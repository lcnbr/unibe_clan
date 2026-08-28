package history

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex-usage-dashboard/internal/model"
)

func historySnapshot(username string, observedAt time.Time, used int, resetAt int64) model.Snapshot {
	email := "private@example.com"
	duration := mainWeekMinutes
	resetCredits := int64(3)
	lifetime := int64(7_777_777)
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
		ResetCreditsAvailable: &resetCredits,
		LifetimeTokens:        &lifetime,
		LifetimeTokensRead:    true,
		RecentThreadsRead:     true,
		RecentThreads: []model.RecentThread{{
			ThreadID: "thread-private-id", TaskName: "Private task name",
		}},
		Limits:     []model.RateLimit{},
		ObservedAt: observedAt,
	}
	snapshot.Normalize()
	return snapshot
}

func TestSlidingZeroWindowNeverBecomesAnchored(t *testing.T) {
	tracker, err := Open("", 56*24*time.Hour)
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
	if got.Revision != initialRevision+1 {
		t.Fatalf("dynamic account registration changed revision from %d to %d", initialRevision, got.Revision)
	}
	if got.Accounts[0].Active != nil || len(got.Accounts[0].Events) != 0 {
		t.Fatalf("sliding zero window became history: %#v", got.Accounts[0])
	}
}

func TestFixedZeroAnchorsAndCompletesExactlyOnce(t *testing.T) {
	tracker, err := Open("", 56*24*time.Hour)
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
	tracker, err := Open(path, 56*24*time.Hour)
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
	for _, historyPath := range []string{path, adjustmentPathFor(path)} {
		payload, err := os.ReadFile(historyPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range [][]byte{
			[]byte("private@example.com"),
			[]byte("lcnbr"),
			[]byte("username"),
			[]byte("Spark"),
			[]byte("credits"),
			[]byte("resetCreditsAvailable"),
			[]byte("accountId"),
			[]byte("lifetimeTokens"),
			[]byte("tokens"),
			[]byte("7777777"),
			[]byte("users"),
			[]byte("membership"),
			[]byte("activeChats"),
			[]byte("recentThreads"),
			[]byte("taskName"),
			[]byte("threadId"),
			[]byte("Private task name"),
			[]byte("thread-private-id"),
		} {
			if bytes.Contains(payload, forbidden) {
				t.Fatalf("history contains forbidden account data %q", forbidden)
			}
		}
	}

	reloaded, err := Open(path, 56*24*time.Hour)
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

func TestFreshAccountHistoryFilesLeaveLegacyFilesUntouched(t *testing.T) {
	directory := t.TempDir()
	legacyHistory := filepath.Join(directory, "history.json")
	legacyAdjustments := filepath.Join(directory, "history-adjustments.json")
	legacyTime := time.Unix(1_700_000_000, 0).UTC()
	legacyPayloads := map[string][]byte{
		legacyHistory:     []byte("legacy username-keyed history\n"),
		legacyAdjustments: []byte("legacy username-keyed adjustments\n"),
	}
	for path, payload := range legacyPayloads {
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, legacyTime, legacyTime); err != nil {
			t.Fatal(err)
		}
	}

	accountHistory := filepath.Join(directory, "account-history.json")
	if _, err := Open(accountHistory, 366*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{accountHistory, filepath.Join(directory, "account-adjustments.json")} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("new account history file %q: info=%v err=%v", path, info, err)
		}
	}
	for path, want := range legacyPayloads {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) || !info.ModTime().Equal(legacyTime) {
			t.Fatalf("legacy file changed: path=%q payload=%q mtime=%v", path, got, info.ModTime())
		}
	}
}

func TestDuplicateAccountObservationDoesNotCreateResetOrAdjustment(t *testing.T) {
	tracker, err := Open("", 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tracker.now = func() time.Time { return now }
	snapshot := historySnapshot("canonical-user", now, 35, now.Add(6*24*time.Hour).Unix())
	tracker.Observe(snapshot)
	first := tracker.Snapshot()
	tracker.Observe(snapshot)
	second := tracker.Snapshot()
	if len(second.Accounts) != 1 || second.Accounts[0].Active == nil ||
		len(second.Accounts[0].Events) != 0 || len(second.Accounts[0].Adjustments) != 0 ||
		second.Revision != first.Revision {
		t.Fatalf("duplicate observation changed history: before=%#v after=%#v", first, second)
	}
}

func TestPreExpiryResetChangePersistsWithoutChangingV1CoreSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	tracker, err := Open(path, 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	serverNow := start
	tracker.now = func() time.Time { return serverNow }
	oldReset := start.Add(6 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("lcnbr", start, 42, oldReset))

	serverNow = start.Add(time.Hour)
	reportedReset := serverNow.Add(7 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("lcnbr", serverNow, 0, reportedReset))
	for index := 1; index <= 8; index++ {
		serverNow = serverNow.Add(30 * time.Second)
		tracker.Observe(historySnapshot("lcnbr", serverNow, 0, reportedReset+int64(index*30)))
	}

	got := tracker.Snapshot().Accounts[0]
	if got.Active != nil || len(got.Events) != 0 || len(got.Adjustments) != 1 {
		t.Fatalf("adjusted history = %#v", got)
	}
	adjustment := got.Adjustments[0]
	if adjustment.Before.ResetsAt != oldReset || adjustment.After.ResetsAt != reportedReset ||
		adjustment.Before.UsedPercent != 42 || adjustment.After.UsedPercent != 0 ||
		len(adjustment.Reasons) != 2 || adjustment.Reasons[0] != AdjustmentResetTimestampChanged ||
		adjustment.Reasons[1] != AdjustmentUsedPercentDecreased {
		t.Fatalf("adjustment = %#v", adjustment)
	}
	apiPayload, err := json.Marshal(tracker.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(apiPayload, []byte("coreRevisionBefore")) {
		t.Fatal("public history response exposes sidecar recovery metadata")
	}

	corePayload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(corePayload, []byte("adjustment")) || bytes.Contains(corePayload, []byte("reasons")) {
		t.Fatal("v1 core history contains sidecar fields")
	}
	core, err := loadState(path)
	if err != nil || core.SchemaVersion != mainDiskSchemaVersion || validateDiskState(core) != nil {
		t.Fatalf("strict v1 core reload failed: state=%#v err=%v", core, err)
	}

	sidecarInfo, err := os.Stat(adjustmentPathFor(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := sidecarInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("adjustment sidecar mode = %o, want 600", got)
	}
	reloaded, err := Open(path, 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Snapshot().Accounts[0]; len(got.Adjustments) != 1 || got.Active != nil {
		t.Fatalf("reloaded adjustments = %#v", got)
	}
}

func TestInferredEarlyResetPointIsDerivedFromPersistedAdjustment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account-history.json")
	tracker, err := Open(path, 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	now := start
	tracker.now = func() time.Time { return now }
	oldReset := start.Add(6 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("collector-a", start, 82, oldReset))

	now = start.Add(2 * time.Hour)
	newReset := now.Add(7 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("collector-a", now, 0, newReset))
	got := tracker.Snapshot().Accounts[0]
	if len(got.Events) != 0 || len(got.Adjustments) != 1 || len(got.ResetPoints) != 1 {
		t.Fatalf("early reset history = %#v", got)
	}
	point := got.ResetPoints[0]
	if point.Kind != ResetPointInferredEarly || point.At != now.Unix() ||
		!point.DetectedAt.Equal(now) || point.UsedPercentBefore != 82 ||
		point.PreviousScheduledAt == nil || *point.PreviousScheduledAt != oldReset ||
		point.NextScheduledAt == nil || *point.NextScheduledAt != newReset {
		t.Fatalf("inferred reset point = %#v", point)
	}

	// Reset points are a public projection. Existing disk schema v2 remains
	// unchanged and the point is recovered from its retained adjustment.
	for _, historyPath := range []string{path, adjustmentPathFor(path)} {
		payload, err := os.ReadFile(historyPath)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(payload, []byte("resetPoints")) || !bytes.Contains(payload, []byte(`"schemaVersion":2`)) {
			t.Fatalf("reset-point projection changed disk schema: %s", payload)
		}
	}
	reloaded, err := Open(path, 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reloadedPoints := reloaded.Snapshot().Accounts[0].ResetPoints
	if len(reloadedPoints) != 1 || reloadedPoints[0].Kind != ResetPointInferredEarly ||
		reloadedPoints[0].At != now.Unix() {
		t.Fatalf("persisted adjustment did not recover reset point: %#v", reloadedPoints)
	}
}

func TestResetPointInferenceRequiresPlausibleNewWindow(t *testing.T) {
	detected := time.Now().UTC().Truncate(time.Second)
	base := storedAdjustment{Adjustment: Adjustment{
		DetectedAt: detected,
		Reasons:    []string{AdjustmentResetTimestampChanged, AdjustmentUsedPercentDecreased},
		Before: WindowObservation{
			WindowStartedAt: detected.Add(-6 * 24 * time.Hour).Unix(),
			ResetsAt:        detected.Add(24 * time.Hour).Unix(),
			FirstObservedAt: detected.Add(-time.Hour),
			UsedPercent:     80,
		},
		After: WindowObservation{
			WindowStartedAt: detected.Add(-30 * time.Minute).Unix(),
			ResetsAt:        detected.Add(6*24*time.Hour + 23*time.Hour + 30*time.Minute).Unix(),
			FirstObservedAt: detected,
			UsedPercent:     3,
		},
	}, CoreRevisionBefore: 1}
	if point, ok := inferredResetPoint(base); !ok || point.At != base.After.WindowStartedAt {
		t.Fatalf("delayed but plausible reset was not inferred: point=%#v ok=%v", point, ok)
	}

	tests := []struct {
		name   string
		mutate func(*storedAdjustment)
	}{
		{
			name: "timestamp only",
			mutate: func(value *storedAdjustment) {
				value.Reasons = []string{AdjustmentResetTimestampChanged}
				value.After.UsedPercent = value.Before.UsedPercent
			},
		},
		{
			name: "usage only",
			mutate: func(value *storedAdjustment) {
				value.Reasons = []string{AdjustmentUsedPercentDecreased}
				value.After.ResetsAt = value.Before.ResetsAt
				value.After.WindowStartedAt = value.Before.WindowStartedAt
			},
		},
		{
			name: "window start predates replaced observation",
			mutate: func(value *storedAdjustment) {
				value.After.WindowStartedAt = value.Before.FirstObservedAt.Add(-time.Second).Unix()
				value.After.ResetsAt = value.After.WindowStartedAt + mainWeekMinutes*60
			},
		},
		{
			name: "window start too old",
			mutate: func(value *storedAdjustment) {
				value.After.WindowStartedAt = detected.Add(-time.Duration(resetObservationMaxLag)*time.Second - time.Second).Unix()
				value.After.ResetsAt = value.After.WindowStartedAt + mainWeekMinutes*60
			},
		},
		{
			name: "window start too far in future",
			mutate: func(value *storedAdjustment) {
				value.After.WindowStartedAt = detected.Add(time.Duration(resetObservationFutureSkew)*time.Second + time.Second).Unix()
				value.After.ResetsAt = value.After.WindowStartedAt + mainWeekMinutes*60
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Reasons = append([]string(nil), base.Reasons...)
			test.mutate(&candidate)
			if point, ok := inferredResetPoint(candidate); ok {
				t.Fatalf("non-reset adjustment produced point %#v", point)
			}
		})
	}
}

func TestResetPointsIncludeScheduledEventsAndPreferThemWhenDeduplicating(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	resetAt := now.Add(-time.Hour).Unix()
	event := ResetEvent{
		WindowStartedAt: resetAt - mainWeekMinutes*60,
		ResetsAt:        resetAt, DetectedAt: now, UsedPercentBefore: 64,
	}
	adjustment := storedAdjustment{Adjustment: Adjustment{
		DetectedAt: now,
		Reasons:    []string{AdjustmentResetTimestampChanged, AdjustmentUsedPercentDecreased},
		Before: WindowObservation{
			WindowStartedAt: resetAt - mainWeekMinutes*60,
			ResetsAt:        resetAt + int64((2*time.Hour)/time.Second), FirstObservedAt: now.Add(-time.Hour), UsedPercent: 64,
		},
		After: WindowObservation{
			WindowStartedAt: resetAt + resetTimestampJitterSeconds,
			ResetsAt:        resetAt + resetTimestampJitterSeconds + mainWeekMinutes*60,
			FirstObservedAt: now, UsedPercent: 0,
		},
	}, CoreRevisionBefore: 1}
	points := publicResetPoints([]ResetEvent{event}, []storedAdjustment{adjustment})
	if len(points) != 1 || points[0].Kind != ResetPointScheduled || points[0].At != resetAt {
		t.Fatalf("reset-point deduplication = %#v", points)
	}
}

func TestRebaselinePreservesUnambiguousEarlyReset(t *testing.T) {
	tracker, err := Open("", 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	now := start
	tracker.now = func() time.Time { return now }
	oldReset := start.Add(5 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("first-source", start, 75, oldReset))

	now = start.Add(time.Hour)
	newReset := now.Add(7 * 24 * time.Hour).Unix()
	tracker.Rebaseline(historySnapshot("replacement-source", now, 0, newReset))
	got := tracker.Snapshot().Accounts[0]
	if len(got.Adjustments) != 1 || len(got.ResetPoints) != 1 ||
		got.ResetPoints[0].Kind != ResetPointInferredEarly || got.ResetPoints[0].At != now.Unix() {
		t.Fatalf("rebaseline erased an unambiguous early reset: %#v", got)
	}
}

func TestRebaselineDoesNotInferResetBeforePreviousObservation(t *testing.T) {
	tracker, err := Open("", 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	now := start
	tracker.now = func() time.Time { return now }
	oldReset := start.Add(6 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("first-source", start, 80, oldReset))

	// This replacement's derived start is 22 hours before the prior window was
	// observed. It falls inside the broad 24-hour lag allowance, but cannot be a
	// reset transition between the two observations.
	now = start.Add(time.Hour)
	newReset := start.Add(6*24*time.Hour + 2*time.Hour).Unix()
	tracker.Rebaseline(historySnapshot("replacement-source", now, 20, newReset))
	got := tracker.Snapshot().Accounts[0]
	if got.Active == nil || got.Active.ResetsAt != newReset || got.Active.UsedPercent != 20 ||
		len(got.Adjustments) != 0 || len(got.ResetPoints) != 0 {
		t.Fatalf("source discrepancy became an inferred reset: %#v", got)
	}
}

func TestSameResetUsageDecreaseIsAdjustment(t *testing.T) {
	tracker, err := Open("", 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	serverNow := start
	tracker.now = func() time.Time { return serverNow }
	resetAt := start.Add(6 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("codex", start, 40, resetAt))
	serverNow = start.Add(time.Hour)
	tracker.Observe(historySnapshot("codex", serverNow, 12, resetAt))
	tracker.Observe(historySnapshot("codex", serverNow.Add(time.Second), 12, resetAt))

	got := tracker.Snapshot().Accounts[0]
	if got.Active == nil || got.Active.UsedPercent != 12 || len(got.Adjustments) != 1 {
		t.Fatalf("usage revision = %#v", got)
	}
	adjustment := got.Adjustments[0]
	if len(adjustment.Reasons) != 1 || adjustment.Reasons[0] != AdjustmentUsedPercentDecreased ||
		adjustment.Before.ResetsAt != adjustment.After.ResetsAt {
		t.Fatalf("usage adjustment = %#v", adjustment)
	}
}

func TestResetTimestampJitterIsIgnored(t *testing.T) {
	tracker, err := Open("", 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	serverNow := start
	tracker.now = func() time.Time { return serverNow }
	resetAt := start.Add(6 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("codex", start, 30, resetAt))

	for index, offset := range []int64{1, -1, resetTimestampJitterSeconds, -resetTimestampJitterSeconds} {
		serverNow = start.Add(time.Duration(index+1) * time.Minute)
		tracker.Observe(historySnapshot("codex", serverNow, 30, resetAt+offset))
	}
	got := tracker.Snapshot().Accounts[0]
	if got.Active == nil || got.Active.ResetsAt != resetAt || got.Active.UsedPercent != 30 ||
		len(got.Events) != 0 || len(got.Adjustments) != 0 {
		t.Fatalf("timestamp jitter changed history = %#v", got)
	}
}

func TestResetTimestampJitterPreservesUsageDecrease(t *testing.T) {
	tracker, err := Open("", 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	serverNow := start
	tracker.now = func() time.Time { return serverNow }
	resetAt := start.Add(6 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("codex", start, 30, resetAt))
	serverNow = start.Add(time.Minute)
	tracker.Observe(historySnapshot("codex", serverNow, 20, resetAt+1))

	got := tracker.Snapshot().Accounts[0]
	if got.Active == nil || got.Active.ResetsAt != resetAt || got.Active.UsedPercent != 20 ||
		len(got.Adjustments) != 1 {
		t.Fatalf("jittered usage decrease = %#v", got)
	}
	adjustment := got.Adjustments[0]
	if len(adjustment.Reasons) != 1 || adjustment.Reasons[0] != AdjustmentUsedPercentDecreased ||
		adjustment.Before.ResetsAt != resetAt || adjustment.After.ResetsAt != resetAt {
		t.Fatalf("jittered usage adjustment = %#v", adjustment)
	}
}

func TestResetTimestampShiftBeyondJitterIsAdjustment(t *testing.T) {
	tracker, err := Open("", 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	serverNow := start
	tracker.now = func() time.Time { return serverNow }
	resetAt := start.Add(6 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("codex", start, 30, resetAt))
	serverNow = start.Add(time.Minute)
	shiftedReset := resetAt + resetTimestampJitterSeconds + 1
	tracker.Observe(historySnapshot("codex", serverNow, 30, shiftedReset))

	got := tracker.Snapshot().Accounts[0]
	if got.Active == nil || got.Active.ResetsAt != shiftedReset || len(got.Adjustments) != 1 ||
		len(got.Events) != 0 {
		t.Fatalf("meaningful timestamp shift = %#v", got)
	}
	if reasons := got.Adjustments[0].Reasons; len(reasons) != 1 ||
		reasons[0] != AdjustmentResetTimestampChanged {
		t.Fatalf("meaningful timestamp shift reasons = %#v", reasons)
	}
}

func TestJitterAtScheduledBoundaryDoesNotCreateDuplicateWindow(t *testing.T) {
	tracker, err := Open("", 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	serverNow := start
	tracker.now = func() time.Time { return serverNow }
	resetAt := start.Add(2 * time.Minute).Unix()
	tracker.Observe(historySnapshot("codex", start, 45, resetAt))

	serverNow = time.Unix(resetAt, 0)
	tracker.Observe(historySnapshot("codex", serverNow, 45, resetAt+1))
	intermediate := tracker.Snapshot().Accounts[0]
	if intermediate.Active == nil || intermediate.Active.ResetsAt != resetAt ||
		len(intermediate.Events) != 0 || len(intermediate.Adjustments) != 0 {
		t.Fatalf("boundary jitter created a window = %#v", intermediate)
	}

	serverNow = serverNow.Add(time.Second)
	nextReset := serverNow.Add(7 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("codex", serverNow, 1, nextReset))
	got := tracker.Snapshot().Accounts[0]
	if got.Active == nil || got.Active.ResetsAt != nextReset || len(got.Events) != 1 ||
		got.Events[0].ResetsAt != resetAt || len(got.Adjustments) != 0 {
		t.Fatalf("next weekly window after jitter = %#v", got)
	}
}

func TestNewWindowInsideEarlyBoundaryJitterCompletesNormally(t *testing.T) {
	for _, lead := range []time.Duration{time.Second, time.Duration(resetTimestampJitterSeconds) * time.Second} {
		t.Run(lead.String(), func(t *testing.T) {
			tracker, err := Open("", 56*24*time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now().UTC().Truncate(time.Second)
			serverNow := start
			tracker.now = func() time.Time { return serverNow }
			resetAt := start.Add(2 * time.Minute).Unix()
			tracker.Observe(historySnapshot("codex", start, 45, resetAt))

			serverNow = time.Unix(resetAt, 0).Add(-lead)
			nextReset := serverNow.Add(7 * 24 * time.Hour).Unix()
			tracker.Observe(historySnapshot("codex", serverNow, 1, nextReset))
			got := tracker.Snapshot().Accounts[0]
			if got.Active == nil || got.Active.ResetsAt != nextReset || len(got.Events) != 1 ||
				got.Events[0].ResetsAt != resetAt || len(got.Adjustments) != 0 {
				t.Fatalf("early boundary jitter %s = %#v", lead, got)
			}
		})
	}
}

func TestOpenWaitsForResetTimestampJitterBeforePruning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	now := time.Now().UTC()
	resetAt := now.Unix() - 1
	state := diskState{
		SchemaVersion: mainDiskSchemaVersion,
		Revision:      7,
		TrackingSince: now.Add(-24 * time.Hour),
		Accounts: map[string]diskAccount{
			model.AccountKey("private@example.com"): {
				Active: &ResetWindow{
					WindowStartedAt: resetAt - mainWeekMinutes*60,
					ResetsAt:        resetAt,
					FirstObservedAt: now.Add(-6 * 24 * time.Hour),
					UsedPercent:     45,
				},
				Events: []ResetEvent{},
			},
		},
	}
	if err := writeState(path, state); err != nil {
		t.Fatal(err)
	}

	opened, err := Open(path, 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got := opened.Snapshot().Accounts[0]
	if got.Active == nil || got.Active.ResetsAt != resetAt || len(got.Events) != 0 {
		t.Fatalf("open pruned inside jitter interval = %#v", got)
	}
}

func TestFixedZeroToSlidingZeroCreatesOneAdjustment(t *testing.T) {
	tracker, err := Open("", 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	serverNow := start
	tracker.now = func() time.Time { return serverNow }
	fixedReset := start.Add(7 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("vhirschi", start, 0, fixedReset))
	serverNow = start.Add(2 * time.Minute)
	tracker.Observe(historySnapshot("vhirschi", serverNow, 0, fixedReset))
	if tracker.Snapshot().Accounts[0].Active == nil {
		t.Fatal("fixed zero did not anchor")
	}

	for index := 0; index < 10; index++ {
		serverNow = start.Add(time.Duration(5+index) * 30 * time.Second)
		reportedReset := serverNow.Add(7 * 24 * time.Hour).Unix()
		tracker.Observe(historySnapshot("vhirschi", serverNow, 0, reportedReset))
	}
	got := tracker.Snapshot().Accounts[0]
	if got.Active != nil || len(got.Adjustments) != 1 || len(got.Events) != 0 {
		t.Fatalf("fixed-to-sliding history = %#v", got)
	}
	if reasons := got.Adjustments[0].Reasons; len(reasons) != 1 || reasons[0] != AdjustmentResetTimestampChanged {
		t.Fatalf("fixed-to-sliding reasons = %#v", reasons)
	}
}

func TestExactExpiryIsCompletionNotAdjustment(t *testing.T) {
	tracker, err := Open("", 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	serverNow := start
	tracker.now = func() time.Time { return serverNow }
	resetAt := start.Add(2 * time.Minute).Unix()
	tracker.Observe(historySnapshot("codex", start, 45, resetAt))
	serverNow = time.Unix(resetAt, 0).Add(time.Second)
	tracker.Observe(historySnapshot("codex", serverNow, 1, serverNow.Add(7*24*time.Hour).Unix()))

	got := tracker.Snapshot().Accounts[0]
	if len(got.Events) != 1 || len(got.Adjustments) != 0 || got.Active == nil {
		t.Fatalf("expiry history = %#v", got)
	}
}

func TestSidecarFirstRetryIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	tracker, err := Open(path, 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	serverNow := start
	tracker.now = func() time.Time { return serverNow }
	oldReset := start.Add(6 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("codex", start, 30, oldReset))

	serverNow = start.Add(time.Hour)
	newReset := serverNow.Add(7 * 24 * time.Hour).Unix()
	accountKey := model.AccountKey("private@example.com")
	active := *tracker.accounts[accountKey].Active
	proposed := ResetWindow{
		WindowStartedAt: newReset - mainWeekMinutes*60,
		ResetsAt:        newReset,
		FirstObservedAt: serverNow,
		UsedPercent:     0,
	}
	adjustment := storedAdjustment{
		Adjustment: Adjustment{
			DetectedAt: serverNow,
			Reasons:    []string{AdjustmentResetTimestampChanged, AdjustmentUsedPercentDecreased},
			Before:     windowObservation(active),
			After:      windowObservation(proposed),
		},
		CoreRevisionBefore: tracker.coreRevision,
	}
	tracker.adjustments[accountKey], _ = appendAdjustment(tracker.adjustments[accountKey], adjustment)
	tracker.revision++
	tracker.adjustmentsDirty = true
	if err := tracker.persistAdjustmentsLocked(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Open(path, 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if immediate := reloaded.Snapshot().Accounts[0]; len(immediate.Adjustments) != 1 || immediate.Active != nil {
		t.Fatalf("sidecar was not reconciled during open: %#v", immediate)
	}
	reloaded.now = func() time.Time { return serverNow }
	reloaded.Observe(historySnapshot("codex", serverNow, 0, newReset))
	got := reloaded.Snapshot().Accounts[0]
	if len(got.Adjustments) != 1 || got.Active != nil {
		t.Fatalf("sidecar retry was not idempotent: %#v", got)
	}
}

func TestSidecarRecoveryPrecedesExpiredCorePruning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	now := time.Now().UTC().Truncate(time.Second)
	detectedAt := now.Add(-48 * time.Hour)
	oldReset := now.Add(-24 * time.Hour).Unix()
	reportedReset := now.Add(4 * 24 * time.Hour).Unix()
	before := WindowObservation{
		WindowStartedAt: oldReset - mainWeekMinutes*60,
		ResetsAt:        oldReset,
		FirstObservedAt: detectedAt.Add(-time.Hour),
		UsedPercent:     30,
	}
	after := WindowObservation{
		WindowStartedAt: reportedReset - mainWeekMinutes*60,
		ResetsAt:        reportedReset,
		FirstObservedAt: detectedAt,
		UsedPercent:     0,
	}
	core := diskState{
		SchemaVersion: mainDiskSchemaVersion,
		Revision:      7,
		TrackingSince: now.Add(-10 * 24 * time.Hour),
		Accounts: map[string]diskAccount{
			model.AccountKey("private@example.com"): {
				Active: &ResetWindow{
					WindowStartedAt: before.WindowStartedAt,
					ResetsAt:        before.ResetsAt,
					FirstObservedAt: before.FirstObservedAt,
					UsedPercent:     before.UsedPercent,
				},
				Events: []ResetEvent{},
			},
		},
	}
	sidecar := adjustmentDiskState{
		SchemaVersion: adjustmentDiskSchemaVersion,
		Revision:      8,
		TrackingSince: detectedAt,
		Accounts: map[string][]storedAdjustment{
			model.AccountKey("private@example.com"): {{
				Adjustment: Adjustment{
					DetectedAt: detectedAt,
					Reasons:    []string{AdjustmentResetTimestampChanged, AdjustmentUsedPercentDecreased},
					Before:     before,
					After:      after,
				},
				CoreRevisionBefore: 7,
			}},
		},
	}
	if err := writeState(path, core); err != nil {
		t.Fatal(err)
	}
	if err := writeState(adjustmentPathFor(path), sidecar); err != nil {
		t.Fatal(err)
	}

	recovered, err := Open(path, 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got := recovered.Snapshot().Accounts[0]
	if got.Active != nil || len(got.Events) != 0 || len(got.Adjustments) != 1 {
		t.Fatalf("expired crash-gap recovery = %#v", got)
	}
	persisted, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Accounts[model.AccountKey("private@example.com")].Active != nil || len(persisted.Accounts[model.AccountKey("private@example.com")].Events) != 0 {
		t.Fatalf("recovered core was not persisted: %#v", persisted.Accounts[model.AccountKey("private@example.com")])
	}
}

func TestSidecarRecoveryReplaysAdjustmentChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	now := time.Now().UTC().Truncate(time.Second)
	resetAt := now.Add(6 * 24 * time.Hour).Unix()
	before := WindowObservation{
		WindowStartedAt: resetAt - mainWeekMinutes*60,
		ResetsAt:        resetAt,
		FirstObservedAt: now.Add(-time.Hour),
		UsedPercent:     30,
	}
	middle := before
	middle.UsedPercent = 20
	after := middle
	after.UsedPercent = 10
	core := diskState{
		SchemaVersion: mainDiskSchemaVersion,
		Revision:      7,
		TrackingSince: now.Add(-24 * time.Hour),
		Accounts: map[string]diskAccount{
			model.AccountKey("private@example.com"): {
				Active: &ResetWindow{
					WindowStartedAt: before.WindowStartedAt,
					ResetsAt:        before.ResetsAt,
					FirstObservedAt: before.FirstObservedAt,
					UsedPercent:     before.UsedPercent,
				},
				Events: []ResetEvent{},
			},
		},
	}
	sidecar := adjustmentDiskState{
		SchemaVersion: adjustmentDiskSchemaVersion,
		Revision:      9,
		TrackingSince: now.Add(-time.Hour),
		Accounts: map[string][]storedAdjustment{
			model.AccountKey("private@example.com"): {
				{
					Adjustment: Adjustment{
						DetectedAt: now.Add(-2 * time.Minute),
						Reasons:    []string{AdjustmentUsedPercentDecreased},
						Before:     before,
						After:      middle,
					},
					CoreRevisionBefore: 7,
				},
				{
					Adjustment: Adjustment{
						DetectedAt: now.Add(-time.Minute),
						Reasons:    []string{AdjustmentUsedPercentDecreased},
						Before:     middle,
						After:      after,
					},
					CoreRevisionBefore: 7,
				},
			},
		},
	}
	if err := writeState(path, core); err != nil {
		t.Fatal(err)
	}
	if err := writeState(adjustmentPathFor(path), sidecar); err != nil {
		t.Fatal(err)
	}

	recovered, err := Open(path, 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got := recovered.Snapshot().Accounts[0]
	if got.Active == nil || got.Active.UsedPercent != after.UsedPercent ||
		len(got.Events) != 0 || len(got.Adjustments) != 2 {
		t.Fatalf("chained crash-gap recovery = %#v", got)
	}
	persisted, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Accounts[model.AccountKey("private@example.com")].Active == nil ||
		persisted.Accounts[model.AccountKey("private@example.com")].Active.UsedPercent != after.UsedPercent {
		t.Fatalf("chained recovery was not persisted: %#v", persisted.Accounts[model.AccountKey("private@example.com")])
	}
}

func TestOpenRemovesPreviouslyRecordedTimestampJitter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	now := time.Now().UTC().Truncate(time.Second)
	resetAt := now.Add(6 * 24 * time.Hour).Unix()
	before := WindowObservation{
		WindowStartedAt: resetAt - mainWeekMinutes*60,
		ResetsAt:        resetAt,
		FirstObservedAt: now.Add(-time.Hour),
		UsedPercent:     30,
	}
	after := before
	after.WindowStartedAt++
	after.ResetsAt++
	after.FirstObservedAt = now.Add(-2 * time.Minute)
	core := diskState{
		SchemaVersion: mainDiskSchemaVersion,
		Revision:      8,
		TrackingSince: now.Add(-24 * time.Hour),
		Accounts: map[string]diskAccount{
			model.AccountKey("private@example.com"): {
				Active: &ResetWindow{
					WindowStartedAt: after.WindowStartedAt,
					ResetsAt:        after.ResetsAt,
					FirstObservedAt: after.FirstObservedAt,
					UsedPercent:     after.UsedPercent,
				},
				Events: []ResetEvent{},
			},
		},
	}
	sidecar := adjustmentDiskState{
		SchemaVersion: adjustmentDiskSchemaVersion,
		Revision:      8,
		TrackingSince: now.Add(-time.Hour),
		Accounts: map[string][]storedAdjustment{
			model.AccountKey("private@example.com"): {{
				Adjustment: Adjustment{
					DetectedAt: now.Add(-2 * time.Minute),
					Reasons:    []string{AdjustmentResetTimestampChanged},
					Before:     before,
					After:      after,
				},
				CoreRevisionBefore: 7,
			}},
		},
	}
	if err := writeState(path, core); err != nil {
		t.Fatal(err)
	}
	if err := writeState(adjustmentPathFor(path), sidecar); err != nil {
		t.Fatal(err)
	}

	opened, err := Open(path, 56*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := opened.Snapshot().Accounts[0]; len(got.Adjustments) != 0 || got.Active == nil ||
		got.Active.ResetsAt != after.ResetsAt {
		t.Fatalf("compacted jitter history = %#v", got)
	}
	persisted, err := loadAdjustmentState(adjustmentPathFor(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Accounts[model.AccountKey("private@example.com")]; len(got) != 0 {
		t.Fatalf("timestamp jitter remained on disk = %#v", got)
	}
}

func TestResetMoveOutsideBoundaryJitterIsAdjustment(t *testing.T) {
	for _, lead := range []time.Duration{
		time.Duration(resetTimestampJitterSeconds+1) * time.Second,
		4*time.Minute + 59*time.Second,
		5 * time.Minute,
	} {
		t.Run(lead.String(), func(t *testing.T) {
			tracker, err := Open("", 56*24*time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now().UTC().Truncate(time.Second)
			serverNow := start
			tracker.now = func() time.Time { return serverNow }
			oldReset := start.Add(6 * 24 * time.Hour).Unix()
			tracker.Observe(historySnapshot("codex", start, 30, oldReset))
			serverNow = time.Unix(oldReset, 0).Add(-lead)
			newReset := serverNow.Add(7 * 24 * time.Hour).Unix()
			tracker.Observe(historySnapshot("codex", serverNow, 30, newReset))
			got := tracker.Snapshot().Accounts[0]
			if len(got.Adjustments) != 1 || len(got.Events) != 0 || got.Active == nil || got.Active.ResetsAt != newReset {
				t.Fatalf("lead %s history = %#v", lead, got)
			}
		})
	}
}

func TestAddingSidecarLeavesExistingV1BytesUntouched(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "history.json")
	now := time.Now().UTC().Truncate(time.Second)
	resetAt := now.Add(6 * 24 * time.Hour).Unix()
	legacy := diskState{
		SchemaVersion: mainDiskSchemaVersion,
		Revision:      7,
		TrackingSince: now.Add(-24 * time.Hour),
		Accounts: map[string]diskAccount{
			model.AccountKey("private@example.com"): {
				Active: &ResetWindow{
					WindowStartedAt: resetAt - mainWeekMinutes*60,
					ResetsAt:        resetAt,
					FirstObservedAt: now.Add(-time.Hour),
					UsedPercent:     20,
				},
				Events: []ResetEvent{},
			},
		},
	}
	if err := writeState(path, legacy); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, 56*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("creating the adjustment sidecar rewrote v1 history")
	}
	if _, err := loadAdjustmentState(adjustmentPathFor(path)); err != nil {
		t.Fatalf("adjustment sidecar was not initialized: %v", err)
	}
}

func TestCorruptAdjustmentSidecarFailsWithoutOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if _, err := Open(path, 14*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	sidecar := adjustmentPathFor(path)
	original := []byte("{not-json\n")
	if err := os.WriteFile(sidecar, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, 14*24*time.Hour); err == nil {
		t.Fatal("corrupt adjustment sidecar unexpectedly loaded")
	}
	after, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("corrupt adjustment sidecar was modified")
	}
}

func TestPruneKeepsOnlyRetainedCompletedEvents(t *testing.T) {
	tracker, err := Open("", 14*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tracker.now = func() time.Time { return now }
	oldReset := now.Add(-15 * 24 * time.Hour).Unix()
	recentReset := now.Add(-2 * 24 * time.Hour).Unix()
	accountKey := model.AccountKey("private@example.com")
	tracker.accounts[accountKey] = diskAccount{Events: []ResetEvent{
		{WindowStartedAt: oldReset - mainWeekMinutes*60, ResetsAt: oldReset, DetectedAt: now.Add(-15 * 24 * time.Hour)},
		{WindowStartedAt: recentReset - mainWeekMinutes*60, ResetsAt: recentReset, DetectedAt: now.Add(-2 * 24 * time.Hour)},
	}}
	tracker.adjustments[accountKey] = []storedAdjustment{}
	tracker.order = []string{accountKey}

	tracker.Observe(model.Snapshot{})
	events := tracker.Snapshot().Accounts[0].Events
	if len(events) != 1 || events[0].ResetsAt != recentReset {
		t.Fatalf("pruned events = %#v", events)
	}
}

func TestRetentionPrunesAt366Days(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account-history.json")
	tracker, err := Open(path, 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	now := start
	tracker.now = func() time.Time { return now }
	resetAt := start.Add(6 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("canonical", start, 40, resetAt))
	now = start.Add(time.Hour)
	tracker.Observe(historySnapshot("canonical", now, 30, resetAt))
	before := tracker.Snapshot()
	if len(before.Accounts) != 1 || before.Accounts[0].Active == nil ||
		len(before.Accounts[0].Adjustments) != 1 {
		t.Fatalf("test history was not established: %#v", before)
	}

	// No Observe or Rebaseline call occurs after this point. Reading history
	// alone must enforce and persist the retention guarantee.
	now = start.Add(374 * 24 * time.Hour)
	got := tracker.Snapshot()
	if got.RetentionDays != 366 || len(got.Accounts) != 0 {
		t.Fatalf("366-day snapshot pruning = %#v", got)
	}
	reloaded, err := Open(path, 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	persisted := reloaded.Snapshot()
	if len(persisted.Accounts) != 0 {
		t.Fatalf("snapshot pruning was not persisted: %#v", persisted)
	}
	if _, err := Open("", 366*24*time.Hour+time.Second); err == nil {
		t.Fatal("retention above 366 days unexpectedly accepted")
	}
}

func TestCorruptHistoryFailsWithoutOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	original := []byte("{not-json\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, 14*24*time.Hour); err == nil {
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
	tracker, err := Open(path, 14*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ancient := historySnapshot("codex", time.Unix(0, 0).UTC(), 20, 1)
	if err := ancient.Validate(); err == nil {
		t.Fatal("invalid weekly window start unexpectedly validated")
	}
	tracker.Observe(ancient)
	if got := tracker.Snapshot(); len(got.Accounts) != 0 {
		t.Fatalf("ancient snapshot changed history: %#v", got)
	}
	if _, err := Open(path, 14*24*time.Hour); err != nil {
		t.Fatalf("ignored snapshot poisoned persisted state: %v", err)
	}
}

func TestResetHistorySafetyCapsCoverAtLeastTwoPointsPerRetentionDay(t *testing.T) {
	minimum := 2 * 366
	if maxEventsPerAccount < minimum || maxAdjustments < minimum || maxResetPointsPerAccount < minimum {
		t.Fatalf("annual safety caps events=%d adjustments=%d points=%d, want each >= %d",
			maxEventsPerAccount, maxAdjustments, maxResetPointsPerAccount, minimum)
	}
}

func TestAccountAdmissionHonorsDiskCapAndReusesEmptyLane(t *testing.T) {
	tracker, err := Open("", 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tracker.now = func() time.Time { return now }
	resetAt := now.Add(6 * 24 * time.Hour).Unix()
	keys := make([]string, 0, maxHistoryAccounts)
	for index := 0; index < maxHistoryAccounts; index++ {
		snapshot := historySnapshot("collector", now, 1, resetAt)
		email := fmt.Sprintf("account-%03d@example.com", index)
		snapshot.Account.Email = &email
		keys = append(keys, model.AccountKey(email))
		tracker.Observe(snapshot)
	}
	if len(tracker.accounts) != maxHistoryAccounts {
		t.Fatalf("admitted account lanes = %d, want %d", len(tracker.accounts), maxHistoryAccounts)
	}

	overflow := historySnapshot("collector", now, 1, resetAt)
	overflowEmail := "overflow@example.com"
	overflow.Account.Email = &overflowEmail
	overflowKey := model.AccountKey(overflowEmail)
	tracker.Observe(overflow)
	if len(tracker.accounts) != maxHistoryAccounts {
		t.Fatalf("overflow changed account count to %d", len(tracker.accounts))
	}
	if _, admitted := tracker.accounts[overflowKey]; admitted {
		t.Fatal("account beyond the disk cap was admitted")
	}

	// Once a lane has no retained state, normal pruning can remove it without
	// violating retention and the next account can be admitted safely.
	emptyKey := keys[0]
	tracker.accounts[emptyKey] = diskAccount{Events: []ResetEvent{}}
	tracker.adjustments[emptyKey] = []storedAdjustment{}
	tracker.Observe(overflow)
	if len(tracker.accounts) != maxHistoryAccounts {
		t.Fatalf("replacement changed account count to %d", len(tracker.accounts))
	}
	if _, retained := tracker.accounts[emptyKey]; retained {
		t.Fatal("empty expired lane was not pruned")
	}
	if _, admitted := tracker.accounts[overflowKey]; !admitted {
		t.Fatal("new account was not admitted after safe pruning")
	}
}

func TestExpiredZeroCandidateReleasesAccountLane(t *testing.T) {
	tracker, err := Open("", 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tracker.now = func() time.Time { return now }
	resetAt := now.Add(7 * 24 * time.Hour).Unix()
	tracker.Observe(historySnapshot("collector", now, 0, resetAt))
	if len(tracker.accounts) != 1 || len(tracker.candidates) != 1 {
		t.Fatalf("zero-use candidate was not established: accounts=%d candidates=%d",
			len(tracker.accounts), len(tracker.candidates))
	}

	now = now.Add(8 * 24 * time.Hour)
	got := tracker.Snapshot()
	if len(got.Accounts) != 0 || len(tracker.candidates) != 0 {
		t.Fatalf("expired zero-use candidate retained a lane: %#v", got)
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

func TestAppendAdjustmentKeepsRuntimeStateWithinLoadLimit(t *testing.T) {
	adjustments := []storedAdjustment{}
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	for index := 0; index < 1100; index++ {
		detectedAt := base.Add(time.Duration(index) * time.Second)
		beforeReset := detectedAt.Add(6 * 24 * time.Hour).Unix()
		afterReset := detectedAt.Add(7 * 24 * time.Hour).Unix()
		var added bool
		adjustments, added = appendAdjustment(adjustments, storedAdjustment{
			Adjustment: Adjustment{
				DetectedAt: detectedAt,
				Reasons:    []string{AdjustmentResetTimestampChanged},
				Before: WindowObservation{
					WindowStartedAt: beforeReset - mainWeekMinutes*60,
					ResetsAt:        beforeReset,
					FirstObservedAt: detectedAt.Add(-time.Hour),
					UsedPercent:     index % 101,
				},
				After: WindowObservation{
					WindowStartedAt: afterReset - mainWeekMinutes*60,
					ResetsAt:        afterReset,
					FirstObservedAt: detectedAt,
					UsedPercent:     index % 101,
				},
			},
			CoreRevisionBefore: uint64(index + 1),
		})
		if !added {
			t.Fatalf("adjustment %d was not added", index)
		}
	}
	if len(adjustments) != maxAdjustments {
		t.Fatalf("runtime adjustment count = %d, want %d", len(adjustments), maxAdjustments)
	}
	wantOldest := base.Add(time.Duration(1100-maxAdjustments) * time.Second)
	if !adjustments[0].DetectedAt.Equal(wantOldest) {
		t.Fatalf("oldest retained adjustment = %s, want %s", adjustments[0].DetectedAt, wantOldest)
	}
}
