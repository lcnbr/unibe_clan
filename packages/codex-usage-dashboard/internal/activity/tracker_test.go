package activity

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func activityEvent(action Action, sessionID, turnID string) Event {
	return Event{
		SchemaVersion: SchemaVersion,
		Action:        action,
		SessionID:     sessionID,
		TurnID:        turnID,
	}
}

func TestTrackerSessionLifecycleRetainsStoppedOpenChat(t *testing.T) {
	tracker, err := New([]Identity{
		{Username: "alice", UID: 1001},
		{Username: "bob", UID: 1002},
	}, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }

	if err := tracker.Apply(1001, activityEvent(ActionSessionStart, "session-a", "")); err != nil {
		t.Fatal(err)
	}
	opened, ok := tracker.Lookup("alice", "session-a")
	if !ok || opened.Running || !opened.StartedAt.Equal(now) || !opened.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected opened chat: %#v, %v", opened, ok)
	}

	now = now.Add(time.Minute)
	if err := tracker.Apply(1001, activityEvent(ActionStart, "session-a", "turn-1")); err != nil {
		t.Fatal(err)
	}
	started, ok := tracker.Lookup("alice", "session-a")
	if !ok || !started.Running || !started.StartedAt.Equal(opened.StartedAt) || !started.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected running chat: %#v, %v", started, ok)
	}

	now = now.Add(4 * time.Minute)
	if err := tracker.Apply(1001, activityEvent(ActionRefresh, "session-a", "turn-1")); err != nil {
		t.Fatal(err)
	}
	refreshed, ok := tracker.Lookup("alice", "session-a")
	if !ok || !refreshed.Running || !refreshed.StartedAt.Equal(started.StartedAt) || !refreshed.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected refreshed chat: %#v, %v", refreshed, ok)
	}

	// Peer UID is part of the key even when opaque session/turn IDs collide.
	if err := tracker.Apply(1002, activityEvent(ActionStart, "session-a", "turn-1")); err != nil {
		t.Fatal(err)
	}
	if got := tracker.Snapshot(); len(got) != 2 || got[0].Username != "alice" || got[1].Username != "bob" {
		t.Fatalf("activity was not separated by peer identity: %#v", got)
	}
	if err := tracker.Apply(1002, activityEvent(ActionEndTurn, "session-a", "turn-1")); err != nil {
		t.Fatal(err)
	}
	if alice, ok := tracker.Lookup("alice", "session-a"); !ok || !alice.Running {
		t.Fatalf("Bob's stop changed Alice's chat: %#v, %v", alice, ok)
	}

	now = now.Add(time.Minute)
	if err := tracker.Apply(1001, activityEvent(ActionEndTurn, "session-a", "turn-1")); err != nil {
		t.Fatal(err)
	}
	idle, ok := tracker.Lookup("alice", "session-a")
	if !ok || idle.Running || !idle.UpdatedAt.Equal(now) {
		t.Fatalf("Stop did not retain an idle chat: %#v, %v", idle, ok)
	}

	// A normally stopped chat remains available during the no-event lease.
	now = now.Add(30*time.Minute - time.Nanosecond)
	if retained, ok := tracker.Lookup("alice", "session-a"); !ok || retained.Running {
		t.Fatalf("idle chat expired before its lease or resumed without an event: %#v, %v", retained, ok)
	}
	if err := tracker.Apply(1001, activityEvent(ActionEndSession, "session-a", "")); err != nil {
		t.Fatal(err)
	}
	if _, ok := tracker.Lookup("alice", "session-a"); ok {
		t.Fatal("SessionEnd left the chat behind")
	}

	// A missed SessionEnd cannot leave a hook-only idle record forever.
	now = now.Add(time.Nanosecond)
	if err := tracker.Apply(1001, activityEvent(ActionSessionStart, "session-b", "")); err != nil {
		t.Fatal(err)
	}
	if got := tracker.SnapshotAt(now.Add(30 * time.Minute)); len(got) != 0 {
		t.Fatalf("abandoned idle chat survived its lease: %#v", got)
	}
}

func TestTrackerParallelTurnsAggregateIntoOneChat(t *testing.T) {
	tracker, err := New([]Identity{{Username: "alice", UID: 1001}}, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }

	if err := tracker.Apply(1001, activityEvent(ActionSessionStart, "shared-session", "")); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Apply(1001, activityEvent(ActionStart, "shared-session", "root-turn")); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Apply(1001, activityEvent(ActionRefresh, "shared-session", "subagent-turn")); err != nil {
		t.Fatal(err)
	}
	if got := tracker.ForUsername("alice"); len(got) != 1 || !got[0].Running {
		t.Fatalf("parallel turns were not deduplicated: %#v", got)
	}

	now = now.Add(time.Minute)
	if err := tracker.Apply(1001, activityEvent(ActionEndTurn, "shared-session", "root-turn")); err != nil {
		t.Fatal(err)
	}
	if chat, ok := tracker.Lookup("alice", "shared-session"); !ok || !chat.Running {
		t.Fatalf("main Stop hid a running subagent: %#v, %v", chat, ok)
	}

	now = now.Add(time.Minute)
	if err := tracker.Apply(1001, activityEvent(ActionEndTurn, "shared-session", "subagent-turn")); err != nil {
		t.Fatal(err)
	}
	if chat, ok := tracker.Lookup("alice", "shared-session"); !ok || chat.Running || !chat.UpdatedAt.Equal(now) {
		t.Fatalf("last SubagentStop did not leave one idle chat: %#v, %v", chat, ok)
	}
}

func TestTrackerAbandonedRunningExpiryAndRestartCleanup(t *testing.T) {
	identities := []Identity{{Username: "alice", UID: 1001}}
	tracker, err := New(identities, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return base }
	if err := tracker.Apply(1001, activityEvent(ActionSessionStart, "session", "")); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Apply(1001, activityEvent(ActionStart, "session", "turn")); err != nil {
		t.Fatal(err)
	}
	if got := tracker.SnapshotAt(base.Add(30*time.Minute - time.Nanosecond)); len(got) != 1 || !got[0].Running {
		t.Fatalf("running chat expired before its lease: %#v", got)
	}
	if got := tracker.SnapshotAt(base.Add(30 * time.Minute)); len(got) != 0 {
		t.Fatalf("abandoned running chat survived its lease: %#v", got)
	}

	restarted, err := New(identities, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.Snapshot(); len(got) != 0 {
		t.Fatalf("new dashboard tracker restored in-memory state: %#v", got)
	}
}

func TestTrackerParallelTurnExpiryKeepsFreshWork(t *testing.T) {
	tracker, err := New([]Identity{{Username: "alice", UID: 1001}}, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	now := base
	tracker.now = func() time.Time { return now }
	for _, turnID := range []string{"stale-turn", "fresh-turn"} {
		if err := tracker.Apply(1001, activityEvent(ActionStart, "session", turnID)); err != nil {
			t.Fatal(err)
		}
	}
	now = base.Add(20 * time.Minute)
	if err := tracker.Apply(1001, activityEvent(ActionRefresh, "session", "fresh-turn")); err != nil {
		t.Fatal(err)
	}
	if got := tracker.SnapshotAt(base.Add(30 * time.Minute)); len(got) != 1 || !got[0].Running {
		t.Fatalf("fresh parallel work was lost with stale work: %#v", got)
	}
	if got := tracker.SnapshotAt(base.Add(50 * time.Minute)); len(got) != 0 {
		t.Fatalf("last abandoned running turn survived expiry: %#v", got)
	}
}

func TestTrackerPerUserFloodEvictsOldestAndPreservesNewest(t *testing.T) {
	tracker, err := New([]Identity{{Username: "alice", UID: 1001}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	for index := 0; index < MaxActiveChatsPerUser; index++ {
		sessionID := fmt.Sprintf("session-%03d", index)
		if err := tracker.Apply(1001, activityEvent(ActionSessionStart, sessionID, "")); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
	}

	// Activity makes the oldest-started chat recent enough to survive.
	if err := tracker.Apply(1001, activityEvent(ActionStart, "session-000", "turn")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := tracker.Apply(1001, activityEvent(ActionSessionStart, "session-newest", "")); err != nil {
		t.Fatal(err)
	}
	if got := tracker.ForUsername("alice"); len(got) != MaxActiveChatsPerUser {
		t.Fatalf("per-user cap not enforced: got %d chats", len(got))
	}
	if _, ok := tracker.Lookup("alice", "session-001"); ok {
		t.Fatal("oldest unrefreshed chat survived overflow")
	}
	for _, sessionID := range []string{"session-000", "session-newest"} {
		if _, ok := tracker.Lookup("alice", sessionID); !ok {
			t.Fatalf("recent chat %q was evicted", sessionID)
		}
	}
}

func TestTrackerCapacityEvictsIdleBeforeOlderRunningChat(t *testing.T) {
	tracker, err := New([]Identity{{Username: "alice", UID: 1001}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	if err := tracker.Apply(1001, activityEvent(ActionStart, "running-oldest", "turn")); err != nil {
		t.Fatal(err)
	}
	for index := 1; index < MaxActiveChatsPerUser; index++ {
		now = now.Add(time.Second)
		if err := tracker.Apply(1001, activityEvent(ActionSessionStart, fmt.Sprintf("idle-%03d", index), "")); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(time.Second)
	if err := tracker.Apply(1001, activityEvent(ActionSessionStart, "idle-newest", "")); err != nil {
		t.Fatal(err)
	}
	if chat, ok := tracker.Lookup("alice", "running-oldest"); !ok || !chat.Running {
		t.Fatalf("capacity pressure evicted running work: %#v, %v", chat, ok)
	}
	if _, ok := tracker.Lookup("alice", "idle-001"); ok {
		t.Fatal("oldest idle chat survived instead of older running work")
	}
}

func TestTrackerKeepsMoreThanEightSimultaneousChats(t *testing.T) {
	tracker, err := New([]Identity{{Username: "alice", UID: 1001}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 9; index++ {
		if err := tracker.Apply(1001, activityEvent(ActionStart, fmt.Sprintf("session-%03d", index), "turn")); err != nil {
			t.Fatal(err)
		}
	}
	if got := tracker.ForUsername("alice"); len(got) != 9 {
		t.Fatalf("simultaneous hook-only chats = %d, want 9", len(got))
	}
}

func TestTrackerRunningTurnCapUsesBoundedSaturationLease(t *testing.T) {
	tracker, err := New([]Identity{{Username: "alice", UID: 1001}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	for index := 0; index <= MaxRunningTurnsPerChat; index++ {
		turnID := fmt.Sprintf("turn-%03d", index)
		if err := tracker.Apply(1001, activityEvent(ActionStart, "session", turnID)); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
	}
	if got := tracker.ForUsername("alice"); len(got) != 1 || !got[0].Running {
		t.Fatalf("turn flood created duplicate chats: %#v", got)
	}
	for index := 0; index <= MaxRunningTurnsPerChat; index++ {
		turnID := fmt.Sprintf("turn-%03d", index)
		if err := tracker.Apply(1001, activityEvent(ActionEndTurn, "session", turnID)); err != nil {
			t.Fatal(err)
		}
	}
	if chat, ok := tracker.Lookup("alice", "session"); !ok || !chat.Running {
		t.Fatalf("overflowed live turn was underreported: %#v, %v", chat, ok)
	}
	overflowExpiresAt := now.Add(time.Hour - time.Second)
	if got := tracker.SnapshotAt(overflowExpiresAt.Add(-time.Nanosecond)); len(got) != 1 || !got[0].Running {
		t.Fatalf("saturation lease expired early: %#v", got)
	}
	if got := tracker.SnapshotAt(overflowExpiresAt); len(got) != 1 || got[0].Running {
		t.Fatalf("expired saturation lease did not become bounded idle state: %#v", got)
	}
	if got := tracker.SnapshotAt(now.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("saturation lease or idle record survived expiry: %#v", got)
	}
}

func TestTrackerEqualTimestampEvictionIsDeterministic(t *testing.T) {
	tracker, err := New([]Identity{{Username: "alice", UID: 1001}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	for index := 0; index < MaxActiveChatsPerUser; index++ {
		if err := tracker.Apply(1001, activityEvent(ActionSessionStart, fmt.Sprintf("session-%03d", index), "")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tracker.Apply(1001, activityEvent(ActionSessionStart, "session-new", "")); err != nil {
		t.Fatal(err)
	}
	if _, ok := tracker.Lookup("alice", "session-000"); ok {
		t.Fatal("lexicographically first tied chat was not evicted")
	}
	if _, ok := tracker.Lookup("alice", "session-new"); !ok {
		t.Fatal("newly opened tied chat was evicted")
	}
}

func TestTrackerGlobalFloodEvictsOldestAcrossUsers(t *testing.T) {
	const userCount = MaxActiveChats/MaxActiveChatsPerUser + 1
	identities := make([]Identity, 0, userCount)
	for userIndex := 0; userIndex < userCount; userIndex++ {
		identities = append(identities, Identity{Username: fmt.Sprintf("user-%02d", userIndex), UID: uint32(2000 + userIndex)})
	}
	tracker, err := New(identities, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return now }
	for index := 0; index < MaxActiveChats; index++ {
		userIndex := index / MaxActiveChatsPerUser
		if err := tracker.Apply(uint32(2000+userIndex), activityEvent(ActionSessionStart, fmt.Sprintf("session-%03d", index), "")); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
	}
	if got := tracker.Snapshot(); len(got) != MaxActiveChats {
		t.Fatalf("initial global fill = %d chats", len(got))
	}
	newestUser := userCount - 1
	if err := tracker.Apply(uint32(2000+newestUser), activityEvent(ActionSessionStart, "flood-newest", "")); err != nil {
		t.Fatal(err)
	}
	if got := tracker.Snapshot(); len(got) != MaxActiveChats {
		t.Fatalf("global cap not enforced: got %d chats", len(got))
	}
	if _, ok := tracker.Lookup("user-00", "session-000"); ok {
		t.Fatal("globally oldest chat survived overflow")
	}
	if _, ok := tracker.Lookup(fmt.Sprintf("user-%02d", newestUser), "flood-newest"); !ok {
		t.Fatal("new global chat was evicted")
	}
}

func TestTrackerRejectsUntrustedUIDAndIdentifierInjection(t *testing.T) {
	tracker, err := New([]Identity{{Username: "alice", UID: 1001}}, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	valid := activityEvent(ActionStart, "session", "turn")
	if err := tracker.Apply(1002, valid); !errors.Is(err, ErrUntrustedPeer) {
		t.Fatalf("untrusted UID error = %v", err)
	}

	invalidIDs := []string{"", " leading-space", "line\nbreak", "../../path", `quote"break`, "unicode-☃", strings.Repeat("a", MaxIDLength+1)}
	for _, invalid := range invalidIDs {
		if err := tracker.Apply(1001, activityEvent(ActionStart, invalid, "turn")); !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("session ID %q error = %v", invalid, err)
		}
	}
	if err := tracker.Apply(1001, activityEvent(ActionStart, strings.Repeat("a", MaxIDLength), "turn")); err != nil {
		t.Fatalf("maximum-length identifier rejected: %v", err)
	}
	for _, action := range []Action{ActionSessionStart, ActionEndSession} {
		if err := tracker.Apply(1001, activityEvent(action, "session", "unexpected-turn")); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("%s accepted turn ID: %v", action, err)
		}
	}
	for _, action := range []Action{ActionStart, ActionRefresh, ActionEndTurn} {
		if err := tracker.Apply(1001, activityEvent(action, "session", "")); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("%s accepted missing turn ID: %v", action, err)
		}
	}
}

func TestChatJSONCannotExposeRawSessionID(t *testing.T) {
	encoded, err := json.Marshal(Chat{
		Username:  "alice",
		SessionID: "secret-session-sentinel",
		StartedAt: time.Unix(1, 0).UTC(),
		UpdatedAt: time.Unix(2, 0).UTC(),
		Running:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "sentinel") || strings.Contains(string(encoded), "sessionId") || strings.Contains(string(encoded), "turnId") {
		t.Fatalf("raw IDs appeared in JSON: %s", encoded)
	}
}

func TestTrackerConfigurationAndConcurrentRefreshes(t *testing.T) {
	for name, identities := range map[string][]Identity{
		"duplicate UID":      {{Username: "alice", UID: 1}, {Username: "bob", UID: 1}},
		"duplicate username": {{Username: "alice", UID: 1}, {Username: "alice", UID: 2}},
		"invalid username":   {{Username: "bad user", UID: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(identities, time.Minute); err == nil {
				t.Fatal("invalid identity configuration was accepted")
			}
		})
	}
	if _, err := New(nil, 0); err == nil {
		t.Fatal("non-positive expiry was accepted")
	}

	tracker, err := New([]Identity{{Username: "alice", UID: 1}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		worker := i
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				sessionID := fmt.Sprintf("session-%03d-%03d", worker, j)
				if err := tracker.Apply(1, activityEvent(ActionRefresh, sessionID, "turn")); err != nil {
					t.Errorf("refresh: %v", err)
					return
				}
				_ = tracker.Snapshot()
			}
		}()
	}
	workers.Wait()
	if got := tracker.Snapshot(); len(got) != MaxActiveChatsPerUser {
		t.Fatalf("concurrent activity corrupted state: %#v", got)
	}
}
