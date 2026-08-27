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
	"strings"
	"sync"
	"time"

	"codex-usage-dashboard/internal/model"
)

const (
	SchemaVersion               = 2
	mainDiskSchemaVersion       = 1
	adjustmentDiskSchemaVersion = 1
	mainWeekMinutes             = int64(10_080)
	maxStateBytes               = int64(1 << 20)
	maxEventsPerAccount         = 128
	maxAdjustments              = 128
	stableAfter                 = 90 * time.Second
	resetTimestampJitterSeconds = int64((2 * time.Minute) / time.Second)

	AdjustmentResetTimestampChanged = "reset_timestamp_changed"
	AdjustmentUsedPercentDecreased  = "used_percent_decreased"
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

type WindowObservation struct {
	WindowStartedAt int64     `json:"windowStartedAt"`
	ResetsAt        int64     `json:"resetsAt"`
	FirstObservedAt time.Time `json:"firstObservedAt"`
	UsedPercent     int       `json:"usedPercent"`
}

// Adjustment records a change reported by the rate-limit service before the
// currently anchored weekly window was due to reset. Reasons describe the
// observed fields, not why the server changed them.
type Adjustment struct {
	DetectedAt time.Time         `json:"detectedAt"`
	Reasons    []string          `json:"reasons"`
	Before     WindowObservation `json:"before"`
	After      WindowObservation `json:"after"`
}

type storedAdjustment struct {
	Adjustment
	CoreRevisionBefore uint64 `json:"coreRevisionBefore"`
}

type AccountHistory struct {
	Username    string       `json:"username"`
	Active      *ResetWindow `json:"active,omitempty"`
	Events      []ResetEvent `json:"events"`
	Adjustments []Adjustment `json:"adjustments"`
}

type Response struct {
	SchemaVersion            int              `json:"schemaVersion"`
	Revision                 uint64           `json:"revision"`
	GeneratedAt              time.Time        `json:"generatedAt"`
	TrackingSince            time.Time        `json:"trackingSince"`
	AdjustmentsTrackingSince time.Time        `json:"adjustmentsTrackingSince"`
	RetentionDays            int              `json:"retentionDays"`
	Degraded                 bool             `json:"degraded,omitempty"`
	Accounts                 []AccountHistory `json:"accounts"`
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

type adjustmentDiskState struct {
	SchemaVersion int                           `json:"schemaVersion"`
	Revision      uint64                        `json:"revision"`
	TrackingSince time.Time                     `json:"trackingSince"`
	Accounts      map[string][]storedAdjustment `json:"accounts"`
}

type candidate struct {
	window       ResetWindow
	firstSeenAt  time.Time
	lastSeenAt   time.Time
	observations int
}

type Tracker struct {
	mu                       sync.RWMutex
	path                     string
	adjustmentPath           string
	retention                time.Duration
	now                      func() time.Time
	order                    []string
	accounts                 map[string]diskAccount
	adjustments              map[string][]storedAdjustment
	candidates               map[string]candidate
	revision                 uint64
	coreRevision             uint64
	trackingSince            time.Time
	adjustmentsTrackingSince time.Time
	dirty                    bool
	adjustmentsDirty         bool
	degraded                 bool
}

func Open(path string, usernames []string, retention time.Duration) (*Tracker, error) {
	if retention < 7*24*time.Hour || retention > 366*24*time.Hour {
		return nil, errors.New("history retention must be between 7 and 366 days")
	}
	tracker := &Tracker{
		path:           path,
		adjustmentPath: adjustmentPathFor(path),
		retention:      retention,
		now:            time.Now,
		accounts:       make(map[string]diskAccount, len(usernames)),
		adjustments:    make(map[string][]storedAdjustment, len(usernames)),
		candidates:     make(map[string]candidate, len(usernames)),
	}
	seen := make(map[string]bool, len(usernames))
	for _, username := range usernames {
		if username == "" || seen[username] {
			return nil, errors.New("history usernames must be nonempty and unique")
		}
		seen[username] = true
		tracker.order = append(tracker.order, username)
		tracker.accounts[username] = diskAccount{Events: []ResetEvent{}}
		tracker.adjustments[username] = []storedAdjustment{}
	}

	now := tracker.now().UTC()
	if path == "" {
		tracker.trackingSince = now
		tracker.adjustmentsTrackingSince = now
		tracker.revision = 1
		tracker.coreRevision = 1
		return tracker, nil
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("history file must be an absolute path")
	}

	historyChanged := false
	loaded, err := loadState(path)
	if errors.Is(err, os.ErrNotExist) {
		tracker.trackingSince = now
		historyChanged = true
	} else if err != nil {
		return nil, errors.New("cannot load usage history")
	} else if err := validateDiskState(loaded); err != nil {
		return nil, errors.New("cannot load usage history")
	} else {
		tracker.revision = loaded.Revision
		tracker.coreRevision = loaded.Revision
		tracker.trackingSince = loaded.TrackingSince.UTC()
		for _, username := range tracker.order {
			if stored, ok := loaded.Accounts[username]; ok {
				stored.Active = cloneWindow(stored.Active)
				stored.Events = cloneEvents(stored.Events)
				if stored.Events == nil {
					stored.Events = []ResetEvent{}
				}
				tracker.accounts[username] = stored
			} else {
				historyChanged = true
			}
		}
		for username := range loaded.Accounts {
			if _, ok := tracker.accounts[username]; !ok {
				historyChanged = true
			}
		}
	}

	adjustmentsChanged := false
	adjustmentState, err := loadAdjustmentState(tracker.adjustmentPath)
	if errors.Is(err, os.ErrNotExist) {
		tracker.adjustmentsTrackingSince = now
		adjustmentsChanged = true
	} else if err != nil {
		return nil, errors.New("cannot load reset adjustment history")
	} else if err := validateAdjustmentDiskState(adjustmentState); err != nil {
		return nil, errors.New("cannot load reset adjustment history")
	} else {
		if adjustmentState.Revision > tracker.revision {
			tracker.revision = adjustmentState.Revision
		}
		tracker.adjustmentsTrackingSince = adjustmentState.TrackingSince.UTC()
		for _, username := range tracker.order {
			if stored, ok := adjustmentState.Accounts[username]; ok {
				tracker.adjustments[username] = cloneStoredAdjustments(stored)
			} else {
				adjustmentsChanged = true
			}
		}
		for username := range adjustmentState.Accounts {
			if _, ok := tracker.adjustments[username]; !ok {
				adjustmentsChanged = true
			}
		}
	}

	if tracker.sanitizeAdjustmentJitterLocked() {
		adjustmentsChanged = true
	}
	if tracker.reconcileSidecarLocked() {
		historyChanged = true
	}
	historyPruned, adjustmentsPruned := tracker.pruneLocked(now)
	historyChanged = historyChanged || historyPruned
	adjustmentsChanged = adjustmentsChanged || adjustmentsPruned
	if historyChanged || adjustmentsChanged {
		tracker.revision++
		tracker.dirty = historyChanged
		tracker.adjustmentsDirty = adjustmentsChanged
		if err := tracker.persistDirtyLocked(); err != nil {
			return nil, errors.New("cannot compact usage history")
		}
	}
	return tracker, nil
}

func (t *Tracker) Observe(snapshot model.Snapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now().UTC()
	historyChanged, adjustmentsChanged := t.pruneLocked(now)
	if snapshot.State == model.StateOK && snapshot.MainUsage != nil {
		windowChanged, adjustmentChanged := t.observeWindowLocked(snapshot.Username, now, snapshot.MainUsage)
		historyChanged = historyChanged || windowChanged
		adjustmentsChanged = adjustmentsChanged || adjustmentChanged
	}
	if historyChanged || adjustmentsChanged {
		t.revision++
		t.dirty = t.dirty || historyChanged
		t.adjustmentsDirty = t.adjustmentsDirty || adjustmentsChanged
	}
	if !t.dirty && !t.adjustmentsDirty {
		return
	}
	if t.path == "" {
		if t.dirty {
			t.coreRevision = t.revision
		}
		t.dirty = false
		t.adjustmentsDirty = false
		t.degraded = false
		return
	}
	if err := t.persistDirtyLocked(); err != nil {
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
			Username:    username,
			Active:      cloneWindow(stored.Active),
			Events:      cloneEvents(stored.Events),
			Adjustments: publicAdjustments(t.adjustments[username]),
		})
	}
	return Response{
		SchemaVersion:            SchemaVersion,
		Revision:                 t.revision,
		GeneratedAt:              t.now().UTC(),
		TrackingSince:            t.trackingSince,
		AdjustmentsTrackingSince: t.adjustmentsTrackingSince,
		RetentionDays:            int(math.Ceil(t.retention.Hours() / 24)),
		Degraded:                 t.degraded,
		Accounts:                 accounts,
	}
}

func (t *Tracker) observeWindowLocked(username string, receivedAt time.Time, window *model.Window) (bool, bool) {
	if _, ok := t.accounts[username]; !ok || window.WindowDurationMins == nil ||
		*window.WindowDurationMins != mainWeekMinutes || window.ResetsAt == nil {
		return false, false
	}
	resetAt := *window.ResetsAt
	if resetAt <= receivedAt.Unix() ||
		resetAt > receivedAt.Add(7*24*time.Hour+5*time.Minute).Unix() {
		return false, false
	}
	used := window.UsedPercent
	proposed := ResetWindow{
		WindowStartedAt: resetAt - mainWeekMinutes*60,
		ResetsAt:        resetAt,
		FirstObservedAt: receivedAt,
		UsedPercent:     used,
	}
	if err := validateWindow(proposed); err != nil {
		return false, false
	}
	account := t.accounts[username]
	changed := false
	adjusted := false

	if account.Active != nil {
		active := *account.Active
		switch {
		case resetTimestampsEquivalent(active.ResetsAt, resetAt):
			delete(t.candidates, username)
			if used < active.UsedPercent {
				after := active
				after.UsedPercent = used
				adjustments := t.adjustments[username]
				adjustments, adjusted = appendAdjustment(adjustments, storedAdjustment{
					Adjustment: Adjustment{
						DetectedAt: receivedAt,
						Reasons:    []string{AdjustmentUsedPercentDecreased},
						Before:     windowObservation(active),
						After:      windowObservation(after),
					},
					CoreRevisionBefore: t.coreRevision,
				})
				t.adjustments[username] = adjustments
			}
			if active.UsedPercent != used {
				account.Active.UsedPercent = used
				changed = true
			}
			t.accounts[username] = account
			return changed, adjusted
		case meaningfullyBeforeReset(active.ResetsAt, receivedAt.Unix()):
			reasons := []string{AdjustmentResetTimestampChanged}
			if used < active.UsedPercent {
				reasons = append(reasons, AdjustmentUsedPercentDecreased)
			}
			adjustments := t.adjustments[username]
			adjustments, adjusted = appendAdjustment(adjustments, storedAdjustment{
				Adjustment: Adjustment{
					DetectedAt: receivedAt,
					Reasons:    reasons,
					Before:     windowObservation(active),
					After:      windowObservation(proposed),
				},
				CoreRevisionBefore: t.coreRevision,
			})
			t.adjustments[username] = adjustments
			account.Active = nil
			delete(t.candidates, username)
			changed = true
		default:
			appendEvent(&account, ResetEvent{
				WindowStartedAt:   active.WindowStartedAt,
				ResetsAt:          active.ResetsAt,
				DetectedAt:        receivedAt,
				UsedPercentBefore: active.UsedPercent,
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
		return changed, adjusted
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
		return changed, adjusted
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
	return changed, adjusted
}

func (t *Tracker) pruneLocked(now time.Time) (bool, bool) {
	cutoffTime := now.Add(-t.retention)
	cutoff := cutoffTime.Unix()
	historyChanged := false
	for username, account := range t.accounts {
		if account.Active != nil && resetTimestampSettled(account.Active.ResetsAt, now.Unix()) {
			appendEvent(&account, ResetEvent{
				WindowStartedAt:   account.Active.WindowStartedAt,
				ResetsAt:          account.Active.ResetsAt,
				DetectedAt:        now,
				UsedPercentBefore: account.Active.UsedPercent,
			})
			account.Active = nil
			historyChanged = true
		}
		kept := account.Events[:0]
		for _, event := range account.Events {
			if event.ResetsAt >= cutoff {
				kept = append(kept, event)
			} else {
				historyChanged = true
			}
		}
		account.Events = kept
		t.accounts[username] = account
	}

	adjustmentsChanged := false
	for username, adjustments := range t.adjustments {
		kept := adjustments[:0]
		for _, adjustment := range adjustments {
			if !adjustment.DetectedAt.Before(cutoffTime) {
				kept = append(kept, adjustment)
			} else {
				adjustmentsChanged = true
			}
		}
		t.adjustments[username] = kept
	}
	return historyChanged, adjustmentsChanged
}

func (t *Tracker) reconcileSidecarLocked() bool {
	changed := false
	for username, account := range t.accounts {
		if account.Active == nil {
			continue
		}
		before := windowObservation(*account.Active)
		adjustments := t.adjustments[username]
		for _, adjustment := range adjustments {
			if adjustment.CoreRevisionBefore != t.coreRevision || adjustment.Before != before {
				continue
			}
			after := ResetWindow{
				WindowStartedAt: adjustment.After.WindowStartedAt,
				ResetsAt:        adjustment.After.ResetsAt,
				FirstObservedAt: adjustment.After.FirstObservedAt,
				UsedPercent:     adjustment.After.UsedPercent,
			}
			if adjustmentHasReason(adjustment.Adjustment, AdjustmentResetTimestampChanged) &&
				after.UsedPercent == 0 {
				account.Active = nil
			} else {
				account.Active = &after
			}
			delete(t.candidates, username)
			t.accounts[username] = account
			before = adjustment.After
			changed = true
		}
	}
	return changed
}

func (t *Tracker) sanitizeAdjustmentJitterLocked() bool {
	changed := false
	for username, adjustments := range t.adjustments {
		kept := adjustments[:0]
		for _, adjustment := range adjustments {
			if !adjustmentHasReason(adjustment.Adjustment, AdjustmentResetTimestampChanged) ||
				!resetTimestampsEquivalent(adjustment.Before.ResetsAt, adjustment.After.ResetsAt) {
				kept = append(kept, adjustment)
				continue
			}
			changed = true
			if adjustment.After.UsedPercent >= adjustment.Before.UsedPercent {
				continue
			}
			adjustment.Reasons = []string{AdjustmentUsedPercentDecreased}
			adjustment.After.WindowStartedAt = adjustment.Before.WindowStartedAt
			adjustment.After.ResetsAt = adjustment.Before.ResetsAt
			adjustment.After.FirstObservedAt = adjustment.Before.FirstObservedAt
			kept = append(kept, adjustment)
		}
		if len(kept) == 0 {
			kept = []storedAdjustment{}
		}
		t.adjustments[username] = kept
	}
	return changed
}

func (t *Tracker) persistDirtyLocked() error {
	// The sidecar is written first. If the process stops before the compatible
	// v1 core file is updated, CoreRevisionBefore suppresses the retry duplicate.
	if t.adjustmentsDirty {
		if err := t.persistAdjustmentsLocked(); err != nil {
			return err
		}
	}
	if t.dirty {
		if err := t.persistHistoryLocked(); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tracker) persistHistoryLocked() error {
	state := diskState{
		SchemaVersion: mainDiskSchemaVersion,
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
	t.coreRevision = t.revision
	t.dirty = false
	return nil
}

func (t *Tracker) persistAdjustmentsLocked() error {
	state := adjustmentDiskState{
		SchemaVersion: adjustmentDiskSchemaVersion,
		Revision:      t.revision,
		TrackingSince: t.adjustmentsTrackingSince,
		Accounts:      make(map[string][]storedAdjustment, len(t.order)),
	}
	for _, username := range t.order {
		state.Accounts[username] = cloneStoredAdjustments(t.adjustments[username])
	}
	if err := validateAdjustmentDiskState(state); err != nil {
		return err
	}
	if err := writeState(t.adjustmentPath, state); err != nil {
		return err
	}
	t.adjustmentsDirty = false
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

func loadAdjustmentState(path string) (adjustmentDiskState, error) {
	var state adjustmentDiskState
	if path == "" {
		return state, os.ErrNotExist
	}
	info, err := os.Stat(path)
	if err != nil {
		return state, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxStateBytes || info.Mode().Perm()&0o077 != 0 {
		return state, errors.New("unsafe reset adjustment history file")
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
			return state, errors.New("multiple reset adjustment history values")
		}
		return state, err
	}
	return state, nil
}

func validateDiskState(state diskState) error {
	if state.SchemaVersion != mainDiskSchemaVersion || state.Revision == 0 ||
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

func validateAdjustmentDiskState(state adjustmentDiskState) error {
	if state.SchemaVersion != adjustmentDiskSchemaVersion || state.Revision == 0 ||
		state.TrackingSince.IsZero() || len(state.Accounts) > 128 {
		return errors.New("invalid reset adjustment history header")
	}
	for username, adjustments := range state.Accounts {
		if username == "" || len(username) > 64 || len(adjustments) > maxAdjustments {
			return errors.New("invalid reset adjustment history account")
		}
		for _, adjustment := range adjustments {
			if err := validateAdjustment(adjustment); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateAdjustment(adjustment storedAdjustment) error {
	if adjustment.DetectedAt.IsZero() || adjustment.CoreRevisionBefore == 0 ||
		validateObservation(adjustment.Before) != nil || validateObservation(adjustment.After) != nil ||
		adjustment.Before.FirstObservedAt.After(adjustment.DetectedAt) ||
		adjustment.After.FirstObservedAt.After(adjustment.DetectedAt) ||
		adjustment.Before.ResetsAt <= adjustment.DetectedAt.Unix() ||
		adjustment.After.ResetsAt <= adjustment.DetectedAt.Unix() ||
		adjustment.After.ResetsAt > adjustment.DetectedAt.Add(7*24*time.Hour+5*time.Minute).Unix() {
		return errors.New("invalid reset adjustment")
	}
	resetChanged := adjustment.Before.ResetsAt != adjustment.After.ResetsAt
	usageDecreased := adjustment.After.UsedPercent < adjustment.Before.UsedPercent
	if resetChanged && !adjustment.DetectedAt.Before(time.Unix(adjustment.Before.ResetsAt, 0)) {
		return errors.New("reset adjustment is not before the scheduled boundary")
	}
	if !resetChanged && !usageDecreased {
		return errors.New("reset adjustment does not describe a change")
	}
	wantReasons := make([]string, 0, 2)
	if resetChanged {
		wantReasons = append(wantReasons, AdjustmentResetTimestampChanged)
	}
	if usageDecreased {
		wantReasons = append(wantReasons, AdjustmentUsedPercentDecreased)
	}
	if len(adjustment.Reasons) != len(wantReasons) {
		return errors.New("invalid reset adjustment reasons")
	}
	for index := range wantReasons {
		if adjustment.Reasons[index] != wantReasons[index] {
			return errors.New("invalid reset adjustment reasons")
		}
	}
	return nil
}

func validateObservation(observation WindowObservation) error {
	if observation.WindowStartedAt <= 0 || observation.ResetsAt <= observation.WindowStartedAt ||
		observation.ResetsAt-observation.WindowStartedAt != mainWeekMinutes*60 ||
		observation.FirstObservedAt.IsZero() || observation.UsedPercent < 0 || observation.UsedPercent > 100 {
		return errors.New("invalid reset adjustment observation")
	}
	return nil
}

func adjustmentHasReason(adjustment Adjustment, reason string) bool {
	for _, candidate := range adjustment.Reasons {
		if candidate == reason {
			return true
		}
	}
	return false
}

func resetTimestampsEquivalent(left, right int64) bool {
	if left > right {
		return left-right <= resetTimestampJitterSeconds
	}
	return right-left <= resetTimestampJitterSeconds
}

func resetTimestampSettled(resetAt, now int64) bool {
	return now >= resetAt && now-resetAt >= resetTimestampJitterSeconds
}

func meaningfullyBeforeReset(resetAt, now int64) bool {
	return now < resetAt && resetAt-now > resetTimestampJitterSeconds
}

func validateWindow(window ResetWindow) error {
	if window.WindowStartedAt <= 0 || window.ResetsAt <= window.WindowStartedAt ||
		window.ResetsAt-window.WindowStartedAt != mainWeekMinutes*60 ||
		window.FirstObservedAt.IsZero() || window.UsedPercent < 0 || window.UsedPercent > 100 {
		return errors.New("invalid active history window")
	}
	return nil
}

func writeState(path string, state any) error {
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

func adjustmentPathFor(path string) string {
	if path == "" {
		return ""
	}
	extension := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), extension)
	return filepath.Join(filepath.Dir(path), base+"-adjustments"+extension)
}

func windowObservation(window ResetWindow) WindowObservation {
	return WindowObservation{
		WindowStartedAt: window.WindowStartedAt,
		ResetsAt:        window.ResetsAt,
		FirstObservedAt: window.FirstObservedAt,
		UsedPercent:     window.UsedPercent,
	}
}

func cloneStoredAdjustments(value []storedAdjustment) []storedAdjustment {
	if value == nil {
		return []storedAdjustment{}
	}
	cloned := make([]storedAdjustment, len(value))
	for index := range value {
		cloned[index] = value[index]
		cloned[index].Reasons = append([]string(nil), value[index].Reasons...)
	}
	return cloned
}

func publicAdjustments(value []storedAdjustment) []Adjustment {
	public := make([]Adjustment, len(value))
	for index := range value {
		public[index] = value[index].Adjustment
		public[index].Reasons = append([]string(nil), value[index].Reasons...)
	}
	return public
}

func appendAdjustment(adjustments []storedAdjustment, adjustment storedAdjustment) ([]storedAdjustment, bool) {
	if err := validateAdjustment(adjustment); err != nil {
		return adjustments, false
	}
	for _, existing := range adjustments {
		if existing.CoreRevisionBefore == adjustment.CoreRevisionBefore &&
			existing.Before == adjustment.Before {
			return adjustments, false
		}
	}
	adjustments = append(adjustments, adjustment)
	sort.Slice(adjustments, func(i, j int) bool {
		return adjustments[i].DetectedAt.Before(adjustments[j].DetectedAt)
	})
	if len(adjustments) > maxAdjustments {
		adjustments = append([]storedAdjustment(nil), adjustments[len(adjustments)-maxAdjustments:]...)
	}
	return adjustments, true
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
