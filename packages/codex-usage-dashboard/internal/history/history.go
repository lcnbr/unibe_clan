package history

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"codex-usage-dashboard/internal/model"
)

const (
	SchemaVersion       = 1
	mainWeekMinutes     = int64(10_080)
	maxStateBytes       = int64(1 << 20)
	maxEventsPerAccount = 128
	stableAfter         = 90 * time.Second
)

type ResetWindow struct {
	WindowStartedAt int64     `json:"windowStartedAt"`
	ResetsAt        int64     `json:"resetsAt"`
	FirstObservedAt time.Time `json:"firstObservedAt"`
	UsedPercent     int       `json:"usedPercent"`
}

type ResetEvent struct {
	WindowStartedAt   int64     `json:"windowStartedAt"`
	ResetsAt          int64     `json:"resetsAt"`
	DetectedAt        time.Time `json:"detectedAt"`
	UsedPercentBefore int       `json:"usedPercentBefore"`
}

type AccountHistory struct {
	Username string       `json:"username"`
	Active   *ResetWindow `json:"active,omitempty"`
	Events   []ResetEvent `json:"events"`
}

type Response struct {
	SchemaVersion int              `json:"schemaVersion"`
	Revision      uint64           `json:"revision"`
	GeneratedAt   time.Time        `json:"generatedAt"`
	TrackingSince time.Time        `json:"trackingSince"`
	RetentionDays int              `json:"retentionDays"`
	Degraded      bool             `json:"degraded,omitempty"`
	Accounts      []AccountHistory `json:"accounts"`
}

type diskState struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Revision      uint64                 `json:"revision"`
	TrackingSince time.Time              `json:"trackingSince"`
	Accounts      map[string]diskAccount `json:"accounts"`
}

type diskAccount struct {
	Active *ResetWindow `json:"active,omitempty"`
	Events []ResetEvent `json:"events"`
}

type candidate struct {
	window       ResetWindow
	firstSeenAt  time.Time
	lastSeenAt   time.Time
	observations int
}

type Tracker struct {
	mu            sync.RWMutex
	path          string
	retention     time.Duration
	now           func() time.Time
	order         []string
	accounts      map[string]diskAccount
	candidates    map[string]candidate
	revision      uint64
	trackingSince time.Time
	dirty         bool
	degraded      bool
}

func Open(path string, usernames []string, retention time.Duration) (*Tracker, error) {
	if retention < 7*24*time.Hour || retention > 366*24*time.Hour {
		return nil, errors.New("history retention must be between 7 and 366 days")
	}
	tracker := &Tracker{
		path:       path,
		retention:  retention,
		now:        time.Now,
		accounts:   make(map[string]diskAccount, len(usernames)),
		candidates: make(map[string]candidate, len(usernames)),
	}
	seen := make(map[string]bool, len(usernames))
	for _, username := range usernames {
		if username == "" || seen[username] {
			return nil, errors.New("history usernames must be nonempty and unique")
		}
		seen[username] = true
		tracker.order = append(tracker.order, username)
		tracker.accounts[username] = diskAccount{Events: []ResetEvent{}}
	}

	if path == "" {
		tracker.trackingSince = tracker.now().UTC()
		tracker.revision = 1
		return tracker, nil
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("history file must be an absolute path")
	}
	loaded, err := loadState(path)
	if errors.Is(err, os.ErrNotExist) {
		tracker.trackingSince = tracker.now().UTC()
		tracker.revision = 1
		tracker.dirty = true
		if err := tracker.persistLocked(); err != nil {
			return nil, errors.New("cannot initialize usage history")
		}
		return tracker, nil
	}
	if err != nil {
		return nil, errors.New("cannot load usage history")
	}
	if err := validateDiskState(loaded); err != nil {
		return nil, errors.New("cannot load usage history")
	}

	tracker.revision = loaded.Revision
	tracker.trackingSince = loaded.TrackingSince.UTC()
	changed := false
	for _, username := range tracker.order {
		if stored, ok := loaded.Accounts[username]; ok {
			stored.Active = cloneWindow(stored.Active)
			stored.Events = cloneEvents(stored.Events)
			if stored.Events == nil {
				stored.Events = []ResetEvent{}
			}
			tracker.accounts[username] = stored
		} else {
			changed = true
		}
	}
	for username := range loaded.Accounts {
		if _, ok := tracker.accounts[username]; !ok {
			changed = true
		}
	}
	if tracker.pruneLocked(tracker.now().UTC()) {
		changed = true
	}
	if changed {
		tracker.revision++
		tracker.dirty = true
		if err := tracker.persistLocked(); err != nil {
			return nil, errors.New("cannot compact usage history")
		}
	}
	return tracker, nil
}

func (t *Tracker) Observe(snapshot model.Snapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now().UTC()
	changed := t.pruneLocked(now)
	if snapshot.State == model.StateOK && snapshot.MainUsage != nil {
		changed = t.observeWindowLocked(snapshot.Username, now, snapshot.MainUsage) || changed
	}
	if changed {
		t.revision++
		t.dirty = true
	}
	if !t.dirty {
		return
	}
	if t.path == "" {
		t.dirty = false
		t.degraded = false
		return
	}
	if err := t.persistLocked(); err != nil {
		t.degraded = true
		return
	}
	t.degraded = false
}

func (t *Tracker) Snapshot() Response {
	t.mu.RLock()
	defer t.mu.RUnlock()

	accounts := make([]AccountHistory, 0, len(t.order))
	for _, username := range t.order {
		stored := t.accounts[username]
		accounts = append(accounts, AccountHistory{
			Username: username,
			Active:   cloneWindow(stored.Active),
			Events:   cloneEvents(stored.Events),
		})
	}
	return Response{
		SchemaVersion: SchemaVersion,
		Revision:      t.revision,
		GeneratedAt:   t.now().UTC(),
		TrackingSince: t.trackingSince,
		RetentionDays: int(math.Ceil(t.retention.Hours() / 24)),
		Degraded:      t.degraded,
		Accounts:      accounts,
	}
}

func (t *Tracker) observeWindowLocked(username string, receivedAt time.Time, window *model.Window) bool {
	if _, ok := t.accounts[username]; !ok || window.WindowDurationMins == nil ||
		*window.WindowDurationMins != mainWeekMinutes || window.ResetsAt == nil {
		return false
	}
	resetAt := *window.ResetsAt
	if resetAt <= receivedAt.Unix() ||
		resetAt > receivedAt.Add(7*24*time.Hour+5*time.Minute).Unix() {
		return false
	}
	used := window.UsedPercent
	proposed := ResetWindow{
		WindowStartedAt: resetAt - mainWeekMinutes*60,
		ResetsAt:        resetAt,
		FirstObservedAt: receivedAt,
		UsedPercent:     used,
	}
	if err := validateWindow(proposed); err != nil {
		return false
	}
	account := t.accounts[username]
	changed := false

	if account.Active != nil {
		switch {
		case account.Active.ResetsAt == resetAt:
			delete(t.candidates, username)
			if account.Active.UsedPercent != used {
				account.Active.UsedPercent = used
				changed = true
			}
			t.accounts[username] = account
			return changed
		case receivedAt.Unix() >= account.Active.ResetsAt:
			appendEvent(&account, ResetEvent{
				WindowStartedAt:   account.Active.WindowStartedAt,
				ResetsAt:          account.Active.ResetsAt,
				DetectedAt:        receivedAt,
				UsedPercentBefore: account.Active.UsedPercent,
			})
			account.Active = nil
			changed = true
		}
	}

	if used > 0 {
		proposed.FirstObservedAt = receivedAt
		if account.Active == nil || account.Active.ResetsAt != resetAt ||
			account.Active.UsedPercent != used {
			account.Active = &proposed
			changed = true
		}
		delete(t.candidates, username)
		t.accounts[username] = account
		return changed
	}

	current, ok := t.candidates[username]
	if !ok || current.window.ResetsAt != resetAt {
		t.candidates[username] = candidate{
			window:       proposed,
			firstSeenAt:  receivedAt,
			lastSeenAt:   receivedAt,
			observations: 1,
		}
		t.accounts[username] = account
		return changed
	}
	current.lastSeenAt = receivedAt
	current.observations++
	t.candidates[username] = current
	if current.observations >= 2 && current.lastSeenAt.Sub(current.firstSeenAt) >= stableAfter {
		current.window.FirstObservedAt = current.firstSeenAt
		if account.Active == nil || account.Active.ResetsAt != resetAt {
			account.Active = cloneWindow(&current.window)
			changed = true
		}
		delete(t.candidates, username)
	}
	t.accounts[username] = account
	return changed
}

func (t *Tracker) pruneLocked(now time.Time) bool {
	cutoff := now.Add(-t.retention).Unix()
	changed := false
	for username, account := range t.accounts {
		if account.Active != nil && account.Active.ResetsAt <= now.Unix() {
			appendEvent(&account, ResetEvent{
				WindowStartedAt:   account.Active.WindowStartedAt,
				ResetsAt:          account.Active.ResetsAt,
				DetectedAt:        now,
				UsedPercentBefore: account.Active.UsedPercent,
			})
			account.Active = nil
			changed = true
		}
		kept := account.Events[:0]
		for _, event := range account.Events {
			if event.ResetsAt >= cutoff {
				kept = append(kept, event)
			} else {
				changed = true
			}
		}
		account.Events = kept
		t.accounts[username] = account
	}
	return changed
}

func (t *Tracker) persistLocked() error {
	state := diskState{
		SchemaVersion: SchemaVersion,
		Revision:      t.revision,
		TrackingSince: t.trackingSince,
		Accounts:      make(map[string]diskAccount, len(t.order)),
	}
	for _, username := range t.order {
		account := t.accounts[username]
		state.Accounts[username] = diskAccount{
			Active: cloneWindow(account.Active),
			Events: cloneEvents(account.Events),
		}
	}
	if err := validateDiskState(state); err != nil {
		return err
	}
	if err := writeState(t.path, state); err != nil {
		return err
	}
	t.dirty = false
	return nil
}

func loadState(path string) (diskState, error) {
	var state diskState
	info, err := os.Stat(path)
	if err != nil {
		return state, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxStateBytes || info.Mode().Perm()&0o077 != 0 {
		return state, errors.New("unsafe history file")
	}
	file, err := os.Open(path)
	if err != nil {
		return state, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxStateBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return state, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return state, errors.New("multiple history values")
		}
		return state, err
	}
	return state, nil
}

func validateDiskState(state diskState) error {
	if state.SchemaVersion != SchemaVersion || state.Revision == 0 ||
		state.TrackingSince.IsZero() || len(state.Accounts) > 128 {
		return errors.New("invalid history header")
	}
	for username, account := range state.Accounts {
		if username == "" || len(username) > 64 || len(account.Events) > maxEventsPerAccount {
			return errors.New("invalid history account")
		}
		if account.Active != nil {
			if err := validateWindow(*account.Active); err != nil {
				return err
			}
		}
		for _, event := range account.Events {
			if event.WindowStartedAt <= 0 || event.ResetsAt <= event.WindowStartedAt ||
				event.ResetsAt-event.WindowStartedAt != mainWeekMinutes*60 ||
				event.DetectedAt.IsZero() || event.UsedPercentBefore < 0 || event.UsedPercentBefore > 100 {
				return errors.New("invalid history event")
			}
		}
	}
	return nil
}

func validateWindow(window ResetWindow) error {
	if window.WindowStartedAt <= 0 || window.ResetsAt <= window.WindowStartedAt ||
		window.ResetsAt-window.WindowStartedAt != mainWeekMinutes*60 ||
		window.FirstObservedAt.IsZero() || window.UsedPercent < 0 || window.UsedPercent > 100 {
		return errors.New("invalid active history window")
	}
	return nil
}

func writeState(path string, state diskState) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if int64(len(payload)) > maxStateBytes {
		return errors.New("history file is too large")
	}
	temporary, err := os.CreateTemp(directory, ".history-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := io.Copy(temporary, bytes.NewReader(payload)); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	cleanup = false
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}

func cloneWindow(value *ResetWindow) *ResetWindow {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneEvents(value []ResetEvent) []ResetEvent {
	if value == nil {
		return []ResetEvent{}
	}
	cloned := make([]ResetEvent, len(value))
	copy(cloned, value)
	return cloned
}

func eventExists(events []ResetEvent, resetAt int64) bool {
	for _, event := range events {
		if event.ResetsAt == resetAt {
			return true
		}
	}
	return false
}

func appendEvent(account *diskAccount, event ResetEvent) {
	if eventExists(account.Events, event.ResetsAt) {
		return
	}
	account.Events = append(account.Events, event)
	sort.Slice(account.Events, func(i, j int) bool {
		return account.Events[i].ResetsAt < account.Events[j].ResetsAt
	})
	if len(account.Events) > maxEventsPerAccount {
		account.Events = append([]ResetEvent(nil), account.Events[len(account.Events)-maxEventsPerAccount:]...)
	}
}
