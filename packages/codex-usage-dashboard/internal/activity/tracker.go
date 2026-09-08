package activity

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	MaxIDLength = 128
	// Keep hook-only tracking aligned with the App Server inventory limit. A
	// user can legitimately have more than eight concurrent root/subagent
	// sessions, and evicting the ninth made live work disappear from the UI.
	MaxActiveChatsPerUser = 64
	// Twenty-three configured collectors can each fill their per-user bound
	// without global pressure. The map remains strictly bounded in memory.
	MaxActiveChats         = 2048
	MaxRunningTurnsPerChat = 32
)

var (
	ErrInvalidEvent  = errors.New("invalid activity event")
	ErrUntrustedPeer = errors.New("untrusted activity peer")
)

// Identity binds an operating-system UID to the only username that UID may
// update. The username is never accepted from an activity client.
type Identity struct {
	Username string
	UID      uint32
}

type Action string

const (
	ActionSessionStart Action = "session_start"
	ActionStart        Action = "start"
	ActionRefresh      Action = "refresh"
	ActionEndTurn      Action = "end_turn"
	ActionEndSession   Action = "end_session"
)

// Event is the complete activity protocol. It deliberately has no fields for
// prompts, tool input, paths, repository data, environment data, or a
// client-supplied username or timestamp.
type Event struct {
	SchemaVersion int    `json:"schemaVersion"`
	Action        Action `json:"action"`
	SessionID     string `json:"sessionId"`
	TurnID        string `json:"turnId,omitempty"`
}

func (event Event) Validate() error {
	if event.SchemaVersion != SchemaVersion {
		return ErrInvalidEvent
	}
	if !validOpaqueID(event.SessionID) {
		return ErrInvalidEvent
	}
	switch event.Action {
	case ActionStart, ActionRefresh, ActionEndTurn:
		if !validOpaqueID(event.TurnID) {
			return ErrInvalidEvent
		}
	case ActionSessionStart, ActionEndSession:
		if event.TurnID != "" {
			return ErrInvalidEvent
		}
	default:
		return ErrInvalidEvent
	}
	return nil
}

const SchemaVersion = 1

func validOpaqueID(value string) bool {
	if value == "" || len(value) > MaxIDLength || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '-', char == '_', char == '.', char == ':':
		default:
			return false
		}
	}
	return true
}

// Chat is an in-memory session view for dashboard integration. SessionID is a
// lookup key only; its json tag provides defense in depth against accidentally
// exposing a raw Codex identifier in an HTTP response.
type Chat struct {
	Username  string    `json:"username"`
	SessionID string    `json:"-"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Running   bool      `json:"running"`
}

type chatKey struct {
	username  string
	sessionID string
}

// trackedChat retains only opaque turn identifiers and their daemon-owned
// refresh times. Multiple root/subagent turns can run in one session, but the
// public dashboard always receives one Chat for that username and session.
type trackedChat struct {
	view              Chat
	runningTurns      map[string]time.Time
	overflowUpdatedAt time.Time
}

// Tracker owns only ephemeral chat state. Constructing a new Tracker
// always starts empty; there is intentionally no persistence API.
type Tracker struct {
	mu           sync.Mutex
	byUID        map[uint32]string
	chats        map[chatKey]*trackedChat
	expiresAfter time.Duration
	now          func() time.Time
	changed      chan struct{}
}

func New(identities []Identity, expiresAfter time.Duration) (*Tracker, error) {
	if expiresAfter <= 0 {
		return nil, errors.New("activity expiry must be positive")
	}
	tracker := &Tracker{
		byUID:        make(map[uint32]string, len(identities)),
		chats:        make(map[chatKey]*trackedChat),
		expiresAfter: expiresAfter,
		now:          time.Now,
		changed:      make(chan struct{}, 1),
	}
	seenNames := make(map[string]bool, len(identities))
	for _, identity := range identities {
		if !validUsername(identity.Username) {
			return nil, errors.New("activity identity has an invalid username")
		}
		if seenNames[identity.Username] {
			return nil, errors.New("activity identity has a duplicate username")
		}
		if _, exists := tracker.byUID[identity.UID]; exists {
			return nil, errors.New("activity identity has a duplicate UID")
		}
		seenNames[identity.Username] = true
		tracker.byUID[identity.UID] = identity.Username
	}
	return tracker, nil
}

func validUsername(username string) bool {
	if username == "" || len(username) > 64 || strings.TrimSpace(username) != username {
		return false
	}
	for _, char := range username {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

// Apply authenticates the event by peer UID and updates the corresponding
// user's state using daemon time. Turn IDs are retained only to aggregate
// parallel root/subagent work into one chat per username and session.
func (tracker *Tracker) Apply(uid uint32, event Event) error {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	username, trusted := tracker.byUID[uid]
	if !trusted {
		return ErrUntrustedPeer
	}
	if err := event.Validate(); err != nil {
		return err
	}
	now := tracker.now().UTC()
	tracker.pruneLocked(now)
	key := chatKey{username: username, sessionID: event.SessionID}

	switch event.Action {
	case ActionSessionStart:
		chat := tracker.ensureChatLocked(key, now)
		chat.view.UpdatedAt = now
	case ActionStart, ActionRefresh:
		chat := tracker.ensureChatLocked(key, now)
		tracker.markTurnRunningLocked(chat, event.TurnID, now)
		chat.view.UpdatedAt = now
		chat.view.Running = true
	case ActionEndTurn:
		chat := tracker.ensureChatLocked(key, now)
		delete(chat.runningTurns, event.TurnID)
		chat.view.UpdatedAt = now
		chat.view.Running = len(chat.runningTurns) != 0 || !chat.overflowUpdatedAt.IsZero()
	case ActionEndSession:
		delete(tracker.chats, key)
	}
	tracker.signalLocked()
	return nil
}

func (tracker *Tracker) signalLocked() {
	select {
	case tracker.changed <- struct{}{}:
	default:
	}
}

func (tracker *Tracker) ensureChatLocked(key chatKey, now time.Time) *trackedChat {
	if chat, exists := tracker.chats[key]; exists {
		return chat
	}
	tracker.makeRoomLocked(key.username)
	chat := &trackedChat{
		view: Chat{
			Username:  key.username,
			SessionID: key.sessionID,
			StartedAt: now,
			UpdatedAt: now,
		},
		runningTurns: make(map[string]time.Time),
	}
	tracker.chats[key] = chat
	return chat
}

// makeRoomLocked evicts before inserting, so the event currently being
// handled always survives. Recently refreshed work wins over abandoned work;
// stable key ordering makes equal-timestamp eviction deterministic.
func (tracker *Tracker) makeRoomLocked(username string) {
	for tracker.countUserLocked(username) >= MaxActiveChatsPerUser {
		if key, ok := tracker.oldestLocked(username); ok {
			delete(tracker.chats, key)
		}
	}
	for len(tracker.chats) >= MaxActiveChats {
		if key, ok := tracker.oldestLocked(""); ok {
			delete(tracker.chats, key)
		}
	}
}

func (tracker *Tracker) markTurnRunningLocked(chat *trackedChat, turnID string, now time.Time) {
	if _, exists := chat.runningTurns[turnID]; exists {
		chat.runningTurns[turnID] = now
		return
	}
	if len(chat.runningTurns) < MaxRunningTurnsPerChat {
		chat.runningTurns[turnID] = now
		return
	}
	// Keep every already-tracked live turn. A single bounded saturation lease
	// represents any additional IDs without allowing an event flood to grow
	// memory or make an evicted live turn appear idle prematurely.
	chat.overflowUpdatedAt = now
}

func (tracker *Tracker) countUserLocked(username string) int {
	count := 0
	for key := range tracker.chats {
		if key.username == username {
			count++
		}
	}
	return count
}

func (tracker *Tracker) oldestLocked(username string) (chatKey, bool) {
	var oldestKey chatKey
	var oldest Chat
	found := false
	for key, chat := range tracker.chats {
		if username != "" && key.username != username {
			continue
		}
		if !found || chatOlder(chat.view, key, oldest, oldestKey) {
			oldestKey = key
			oldest = chat.view
			found = true
		}
	}
	return oldestKey, found
}

func chatOlder(left Chat, leftKey chatKey, right Chat, rightKey chatKey) bool {
	if left.Running != right.Running {
		// Capacity pressure discards an idle disclosure before live work even
		// when the running chat has an older timestamp.
		return !left.Running
	}
	if !left.UpdatedAt.Equal(right.UpdatedAt) {
		return left.UpdatedAt.Before(right.UpdatedAt)
	}
	if !left.StartedAt.Equal(right.StartedAt) {
		return left.StartedAt.Before(right.StartedAt)
	}
	if leftKey.username != rightKey.username {
		return leftKey.username < rightKey.username
	}
	if leftKey.sessionID != rightKey.sessionID {
		return leftKey.sessionID < rightKey.sessionID
	}
	return false
}

func (tracker *Tracker) pruneLocked(now time.Time) bool {
	changed := false
	for key, chat := range tracker.chats {
		for turnID, updatedAt := range chat.runningTurns {
			if !now.Before(updatedAt.Add(tracker.expiresAfter)) {
				delete(chat.runningTurns, turnID)
				changed = true
			}
		}
		if !chat.overflowUpdatedAt.IsZero() && !now.Before(chat.overflowUpdatedAt.Add(tracker.expiresAfter)) {
			chat.overflowUpdatedAt = time.Time{}
			changed = true
		}
		chat.view.Running = len(chat.runningTurns) != 0 || !chat.overflowUpdatedAt.IsZero()
		// SessionEnd normally removes a chat immediately. This independent
		// chat-level lease bounds stale SessionStart/Stop records when a client
		// crashes or a terminal hook is missed. Loaded idle chats remain visible
		// through the separate App Server runtime inventory.
		if !chat.view.Running && !now.Before(chat.view.UpdatedAt.Add(tracker.expiresAfter)) {
			delete(tracker.chats, key)
			changed = true
		}
	}
	return changed
}

func (tracker *Tracker) nextExpiryDelay() (time.Duration, bool) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	now := tracker.now().UTC()
	var next time.Time
	for _, chat := range tracker.chats {
		if len(chat.runningTurns) == 0 && chat.overflowUpdatedAt.IsZero() {
			expiresAt := chat.view.UpdatedAt.Add(tracker.expiresAfter)
			if next.IsZero() || expiresAt.Before(next) {
				next = expiresAt
			}
		} else {
			for _, updatedAt := range chat.runningTurns {
				expiresAt := updatedAt.Add(tracker.expiresAfter)
				if next.IsZero() || expiresAt.Before(next) {
					next = expiresAt
				}
			}
			if !chat.overflowUpdatedAt.IsZero() {
				expiresAt := chat.overflowUpdatedAt.Add(tracker.expiresAfter)
				if next.IsZero() || expiresAt.Before(next) {
					next = expiresAt
				}
			}
		}
	}
	if next.IsZero() {
		return 0, false
	}
	remaining := next.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	return remaining, true
}

// runExpiry drives exact lease cleanup while a Server is running. The tracker
// remains usable without it: every read and write also prunes abandoned
// running turns and hook-only idle chats.
func (tracker *Tracker) runExpiry(ctx context.Context, onExpire func()) {
	for {
		delay, scheduled := tracker.nextExpiryDelay()
		if !scheduled {
			select {
			case <-ctx.Done():
				return
			case <-tracker.changed:
				continue
			}
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return
		case <-tracker.changed:
			stopTimer(timer)
			continue
		case <-timer.C:
			tracker.mu.Lock()
			changed := tracker.pruneLocked(tracker.now().UTC())
			tracker.mu.Unlock()
			if changed && onExpire != nil {
				onExpire()
			}
		}
	}
}

func stopTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func (tracker *Tracker) Snapshot() []Chat {
	return tracker.SnapshotAt(tracker.now().UTC())
}

// SnapshotAt returns a deterministic copy and expires abandoned hook records
// at the supplied dashboard time.
func (tracker *Tracker) SnapshotAt(now time.Time) []Chat {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.pruneLocked(now.UTC())
	return tracker.snapshotLocked("")
}

func (tracker *Tracker) ForUsername(username string) []Chat {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.pruneLocked(tracker.now().UTC())
	return tracker.snapshotLocked(username)
}

func (tracker *Tracker) snapshotLocked(username string) []Chat {
	result := make([]Chat, 0, len(tracker.chats))
	for _, chat := range tracker.chats {
		if username == "" || chat.view.Username == username {
			result = append(result, chat.view)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Username != result[j].Username {
			return result[i].Username < result[j].Username
		}
		return result[i].SessionID < result[j].SessionID
	})
	return result
}

func (tracker *Tracker) Lookup(username, sessionID string) (Chat, bool) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.pruneLocked(tracker.now().UTC())
	chat, exists := tracker.chats[chatKey{username: username, sessionID: sessionID}]
	if !exists {
		return Chat{}, false
	}
	return chat.view, true
}
