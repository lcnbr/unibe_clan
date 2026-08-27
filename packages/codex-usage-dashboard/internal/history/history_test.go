package history

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex-usage-dashboard/internal/model"
)

func historySnapshot(username string, observedAt time.Time, used int, resetAt int64) model.Snapshot {
	email := "private@example.com"
	duration := mainWeekMinutes
	snapshot := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      username,
		State:         model.StateOK,
		Account:       &model.Account{Type: "chatgpt", Email: &email, PlanType: "pro"},
		MainUsage: &model.Window{
			UsedPercent:        used,
			WindowDurationMins: &duration,
			ResetsAt:           &resetAt,
		},
		Limits:     []model.RateLimit{},
		ObservedAt: observedAt,
	}
	snapshot.Normalize()
	return snapshot
}

func TestSlidingZeroWindowNeverBecomesAnchored(t *testing.T) {
	tracker, err := Open("", []string{"zeno"}, 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	serverNow := start
	tracker.now = func() time.Time { return serverNow }
	initialRevision := tracker.Snapshot().Revision
	for index := 0; index < 12; index++ {
		observedAt := start.Add(time.Duration(index) * 30 * time.Second)
		serverNow = observedAt
		resetAt := observedAt.Add(7 * 24 * time.Hour).Unix()
		tracker.Observe(historySnapshot("zeno", observedAt, 0, resetAt))
	}
	got := tracker.Snapshot()
	if got.Revision != initialRevision {
		t.Fatalf("sliding zero window changed revision from %d to %d", initialRevision, got.Revision)
	}
	if got.Accounts[0].Active != nil || len(got.Accounts[0].Events) != 0 {
		t.Fatalf("sliding zero window became history: %#v", got.Accounts[0])
	}
}

func TestFixedZeroAnchorsAndCompletesExactlyOnce(t *testing.T) {
	tracker, err := Open("", []string{"vhirschi"}, 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	serverNow := start
	tracker.now = func() time.Time { return serverNow }
	resetAt := start.Add(7 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("vhirschi", start, 0, resetAt))
	serverNow = start.Add(2 * time.Minute)
	tracker.Observe(historySnapshot("vhirschi", start.Add(2*time.Minute), 0, resetAt))

	anchored := tracker.Snapshot().Accounts[0].Active
	if anchored == nil || anchored.ResetsAt != resetAt {
		t.Fatalf("fixed zero window was not anchored: %#v", anchored)
	}

	afterReset := time.Unix(resetAt, 0).Add(30 * time.Second)
	serverNow = afterReset
	slidingReset := afterReset.Add(7 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("vhirschi", afterReset, 0, slidingReset))
	serverNow = afterReset.Add(30 * time.Second)
	tracker.Observe(historySnapshot("vhirschi", afterReset.Add(30*time.Second), 0, slidingReset+30))
	serverNow = afterReset.Add(time.Minute)
	tracker.Observe(historySnapshot("vhirschi", afterReset.Add(time.Minute), 0, slidingReset+60))

	got := tracker.Snapshot().Accounts[0]
	if got.Active != nil {
		t.Fatalf("sliding replacement was anchored: %#v", got.Active)
	}
	if len(got.Events) != 1 || got.Events[0].ResetsAt != resetAt {
		t.Fatalf("completed reset events = %#v", got.Events)
	}
}

func TestPositiveWindowPersistsAndReloadsPrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	tracker, err := Open(path, []string{"lcnbr"}, 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	observedAt := time.Now().UTC().Truncate(time.Second)
	resetAt := observedAt.Add(6 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("lcnbr", observedAt, 37, resetAt))

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("history mode = %o, want 600", got)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{
		[]byte("private@example.com"),
		[]byte("Spark"),
		[]byte("credits"),
		[]byte("accountId"),
	} {
		if bytes.Contains(payload, forbidden) {
			t.Fatalf("history contains forbidden account data %q", forbidden)
		}
	}

	reloaded, err := Open(path, []string{"lcnbr"}, 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.Snapshot()
	if got.Accounts[0].Active == nil || got.Accounts[0].Active.ResetsAt != resetAt ||
		got.Accounts[0].Active.UsedPercent != 37 {
		t.Fatalf("reloaded history = %#v", got.Accounts[0])
	}
	got.Accounts[0].Active.ResetsAt = 1
	if reloaded.Snapshot().Accounts[0].Active.ResetsAt != resetAt {
		t.Fatal("history response was not a defensive copy")
	}
}

func TestPruneKeepsOnlyRetainedCompletedEvents(t *testing.T) {
	tracker, err := Open("", []string{"codex"}, 14*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tracker.now = func() time.Time { return now }
	oldReset := now.Add(-15 * 24 * time.Hour).Unix()
	recentReset := now.Add(-2 * 24 * time.Hour).Unix()
	tracker.accounts["codex"] = diskAccount{Events: []ResetEvent{
		{WindowStartedAt: oldReset - mainWeekMinutes*60, ResetsAt: oldReset, DetectedAt: now.Add(-15 * 24 * time.Hour)},
		{WindowStartedAt: recentReset - mainWeekMinutes*60, ResetsAt: recentReset, DetectedAt: now.Add(-2 * 24 * time.Hour)},
	}}

	tracker.Observe(model.Snapshot{})
	events := tracker.Snapshot().Accounts[0].Events
	if len(events) != 1 || events[0].ResetsAt != recentReset {
		t.Fatalf("pruned events = %#v", events)
	}
}

func TestCorruptHistoryFailsWithoutOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	original := []byte("{not-json\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, []string{"codex"}, 14*24*time.Hour); err == nil {
		t.Fatal("corrupt history unexpectedly loaded")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("corrupt history was modified")
	}
}

func TestAncientCollectorTimestampCannotPoisonState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	tracker, err := Open(path, []string{"codex"}, 14*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	initial := tracker.Snapshot().Revision
	ancient := historySnapshot("codex", time.Unix(0, 0).UTC(), 20, 1)
	if err := ancient.Validate(); err == nil {
		t.Fatal("invalid weekly window start unexpectedly validated")
	}
	tracker.Observe(ancient)
	if got := tracker.Snapshot(); got.Revision != initial || got.Accounts[0].Active != nil {
		t.Fatalf("ancient snapshot changed history: %#v", got)
	}
	if _, err := Open(path, []string{"codex"}, 14*24*time.Hour); err != nil {
		t.Fatalf("ignored snapshot poisoned persisted state: %v", err)
	}
}

func TestAppendEventKeepsRuntimeStateWithinLoadLimit(t *testing.T) {
	account := diskAccount{Events: []ResetEvent{}}
	base := time.Now().UTC().Add(-time.Hour).Unix()
	for index := 0; index < 1100; index++ {
		resetAt := base + int64(index)
		appendEvent(&account, ResetEvent{
			WindowStartedAt:   resetAt - mainWeekMinutes*60,
			ResetsAt:          resetAt,
			DetectedAt:        time.Now().UTC(),
			UsedPercentBefore: index % 101,
		})
	}
	if len(account.Events) != maxEventsPerAccount {
		t.Fatalf("runtime event count = %d, want %d", len(account.Events), maxEventsPerAccount)
	}
	wantOldest := base + 1100 - maxEventsPerAccount
	if account.Events[0].ResetsAt != wantOldest {
		t.Fatalf("oldest retained reset = %d, want %d", account.Events[0].ResetsAt, wantOldest)
	}
}
