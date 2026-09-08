package hub

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	usagehistory "codex-usage-dashboard/internal/history"
	"codex-usage-dashboard/internal/model"
)

func testSnapshot(username string, observedAt time.Time, used int) model.Snapshot {
	email := username + "@example.com"
	duration := int64(300)
	reset := observedAt.Add(2 * time.Hour).Unix()
	weeklyDuration := int64(10_080)
	weeklyReset := observedAt.Add(7 * 24 * time.Hour).Unix()
	resetCredits := int64(2)
	snapshot := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      username,
		State:         model.StateOK,
		Account:       &model.Account{Type: "chatgpt", Email: &email, PlanType: "plus"},
		MainUsage: &model.Window{
			UsedPercent: used, WindowDurationMins: &weeklyDuration, ResetsAt: &weeklyReset,
		},
		ResetCreditsAvailable: &resetCredits,
		ObservedAt:            observedAt,
		Limits: []model.RateLimit{{
			ID:   "codex",
			Name: stringPointer("Codex"),
			Primary: &model.Window{
				UsedPercent:        used,
				WindowDurationMins: &duration,
				ResetsAt:           &reset,
			},
		}},
	}
	snapshot.Normalize()
	return snapshot
}

func stringPointer(value string) *string { return &value }

func withCodexVersion(snapshot model.Snapshot, version string) model.Snapshot {
	observedAt := snapshot.ObservedAt
	snapshot.CodexVersion = version
	snapshot.CodexVersionObservedAt = &observedAt
	return snapshot
}

func TestApplyRejectsSpoofedUsernameAndRawInvalidData(t *testing.T) {
	now := time.Now().UTC()
	state, err := New([]Identity{{Username: "codex", UID: 123}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }

	spoofed := testSnapshot("codex-1", now, 12)
	if err := state.Apply(123, spoofed); err == nil {
		t.Fatal("spoofed username was accepted")
	}
	if err := state.Apply(999, testSnapshot("codex", now, 12)); err == nil {
		t.Fatal("unknown UID was accepted")
	}
	invalid := testSnapshot("codex", now, 12)
	invalid.SchemaVersion = 999
	invalid.Limits[0].Primary.UsedPercent = 100000
	if err := state.Apply(123, invalid); err == nil {
		t.Fatal("raw invalid snapshot was normalized and accepted")
	}
	got := state.Status()
	if len(got.Accounts) != 0 || len(got.UnassignedUsers) != 1 ||
		got.UnassignedUsers[0].State != model.StateUnavailable {
		t.Fatalf("stored state changed after rejection: %#v", got)
	}
}

func TestUnavailableRetainsLastGoodAndBecomesStale(t *testing.T) {
	now := time.Now().UTC()
	state, err := New([]Identity{{Username: "codex", UID: 123}}, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	if err := state.Apply(123, testSnapshot("codex", now, 24)); err != nil {
		t.Fatal(err)
	}

	now = now.Add(30 * time.Second)
	unavailable := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      "codex",
		State:         model.StateUnavailable,
		Limits:        []model.RateLimit{},
		ObservedAt:    now,
		ErrorCategory: model.ErrorCodexUnavailable,
	}
	if err := state.Apply(123, unavailable); err != nil {
		t.Fatal(err)
	}
	status := state.Status().Accounts[0]
	if !status.Stale {
		t.Fatal("transient failure was not marked stale")
	}
	if status.State != model.StateUnavailable || status.Account == nil || status.MainUsage == nil ||
		status.ResetCreditsAvailable == nil || *status.ResetCreditsAvailable != 2 || len(status.Limits) != 1 {
		t.Fatalf("last-good data was not retained: %#v", status)
	}
	if !status.ObservedAt.Equal(now.Add(-30*time.Second)) || !status.LastSeenAt.Equal(now) {
		t.Fatalf("last-good and collector timestamps were conflated: observed=%v seen=%v", status.ObservedAt, status.LastSeenAt)
	}

	now = now.Add(61 * time.Second)
	status = state.Status().Accounts[0]
	if !status.Stale {
		t.Fatal("repeated failure freshness masked stale last-good data")
	}
	if status.LastGoodAt == nil || !status.LastGoodAt.Equal(now.Add(-91*time.Second)) {
		t.Fatalf("unexpected last-good timestamp: %v", status.LastGoodAt)
	}
}

func TestNonMonotonicSnapshotsCannotRollBackAccountOrHistory(t *testing.T) {
	receivedAt := time.Now().UTC().Truncate(time.Second)
	observedAt := receivedAt.Add(-time.Minute)
	state, err := New([]Identity{{Username: "consumer", UID: 124}}, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return receivedAt }
	tracker, err := usagehistory.Open("", 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	state.SetHistory(tracker)

	email := "current@example.com"
	current := withCodexVersion(testSnapshot("consumer", observedAt.Add(20*time.Second), 70), "1.2.3")
	current.Account.Email = &email
	if err := state.Apply(124, current); err != nil {
		t.Fatal(err)
	}
	acceptedRevision := state.Status().Revision
	acceptedAccountVersion := state.entries["consumer"].accountVersion

	assertCurrent := func(wantState model.State) {
		t.Helper()
		status := state.Status()
		if status.Revision != acceptedRevision || len(status.Accounts) != 1 ||
			status.Accounts[0].Account == nil || status.Accounts[0].Account.Email == nil ||
			*status.Accounts[0].Account.Email != email || status.Accounts[0].State != wantState ||
			status.Accounts[0].MainUsage == nil || status.Accounts[0].MainUsage.UsedPercent != 70 ||
			status.Accounts[0].Users[0].CodexVersion != "1.2.3" ||
			state.entries["consumer"].accountVersion != acceptedAccountVersion {
			t.Fatalf("non-monotonic snapshot changed current state: %#v entry=%#v", status, state.entries["consumer"])
		}
		history := tracker.Snapshot()
		if len(history.Accounts) != 1 || history.Accounts[0].AccountKey != model.AccountKey(email) ||
			history.Accounts[0].Active == nil || history.Accounts[0].Active.UsedPercent != 70 ||
			len(history.Accounts[0].Events) != 0 || len(history.Accounts[0].Adjustments) != 0 ||
			len(history.Accounts[0].ResetPoints) != 0 {
			t.Fatalf("non-monotonic snapshot changed history: %#v", history)
		}
	}

	olderAccount := withCodexVersion(testSnapshot("consumer", observedAt.Add(10*time.Second), 5), "0.9.0")
	olderEmail := "older@example.com"
	olderAccount.Account.Email = &olderEmail
	if err := state.Apply(124, olderAccount); err != nil {
		t.Fatalf("older snapshot was not acknowledged: %v", err)
	}
	assertCurrent(model.StateOK)

	duplicateTime := testSnapshot("consumer", current.ObservedAt, 1)
	duplicateTime.Account.Email = &olderEmail
	if err := state.Apply(124, duplicateTime); err != nil {
		t.Fatalf("equal-timestamp snapshot was not acknowledged: %v", err)
	}
	assertCurrent(model.StateOK)

	newerUnavailable := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      "consumer",
		State:         model.StateUnavailable,
		Limits:        []model.RateLimit{},
		ObservedAt:    observedAt.Add(30 * time.Second),
		ErrorCategory: model.ErrorCodexUnavailable,
	}
	if err := state.Apply(124, newerUnavailable); err != nil {
		t.Fatal(err)
	}
	acceptedRevision = state.Status().Revision
	assertCurrent(model.StateUnavailable)

	olderSignedOut := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      "consumer",
		State:         model.StateSignedOut,
		Limits:        []model.RateLimit{},
		ObservedAt:    observedAt.Add(25 * time.Second),
	}
	if err := state.Apply(124, olderSignedOut); err != nil {
		t.Fatalf("older sign-out was not acknowledged: %v", err)
	}
	assertCurrent(model.StateUnavailable)

	newerSignedOut := olderSignedOut
	newerSignedOut.ObservedAt = observedAt.Add(40 * time.Second)
	if err := state.Apply(124, newerSignedOut); err != nil {
		t.Fatal(err)
	}
	status := state.Status()
	if len(status.Accounts) != 0 || len(status.UnassignedUsers) != 1 ||
		status.UnassignedUsers[0].State != model.StateSignedOut ||
		state.entries["consumer"].accountVersion != acceptedAccountVersion+1 {
		t.Fatalf("newer sign-out did not clear membership: %#v entry=%#v", status, state.entries["consumer"])
	}

	// An older OK snapshot arriving after sign-out must not resurrect either the
	// old or a different account.
	if err := state.Apply(124, current); err != nil {
		t.Fatalf("older post-sign-out snapshot was not acknowledged: %v", err)
	}
	status = state.Status()
	if len(status.Accounts) != 0 || len(status.UnassignedUsers) != 1 ||
		status.UnassignedUsers[0].State != model.StateSignedOut {
		t.Fatalf("older snapshot resurrected signed-out membership: %#v", status)
	}
}

func TestUserCodexVersionIsIndependentOfAccountLifecycle(t *testing.T) {
	now := time.Now().UTC()
	state, err := New([]Identity{{Username: "codex", UID: 123}}, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	healthy := withCodexVersion(testSnapshot("codex", now, 24), "0.149.0")
	if err := state.Apply(123, healthy); err != nil {
		t.Fatal(err)
	}
	if got := state.Status().Accounts[0].Users[0].CodexVersion; got != "0.149.0" {
		t.Fatalf("healthy user version = %q", got)
	}

	// An empty optional field from a transient failure must not erase the last
	// version successfully observed from this user's interactive process.
	now = now.Add(time.Second)
	if err := state.Apply(123, model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      "codex",
		State:         model.StateUnavailable,
		Limits:        []model.RateLimit{},
		ObservedAt:    now,
		ErrorCategory: model.ErrorCodexUnavailable,
	}); err != nil {
		t.Fatal(err)
	}
	if got := state.Status().Accounts[0].Users[0].CodexVersion; got != "0.149.0" {
		t.Fatalf("transient metadata failure erased version: %q", got)
	}

	// Version follows the Linux user's executable and updates independently of
	// which account is selected.
	now = now.Add(time.Second)
	switched := withCodexVersion(testSnapshot("codex", now, 10), "0.150.0")
	otherEmail := "other@example.com"
	switched.Account.Email = &otherEmail
	if err := state.Apply(123, switched); err != nil {
		t.Fatal(err)
	}
	status := state.Status()
	if len(status.Accounts) != 1 || status.Accounts[0].Users[0].CodexVersion != "0.150.0" {
		t.Fatalf("switched user version = %#v", status)
	}

	now = now.Add(time.Second)
	if err := state.Apply(123, model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      "codex",
		State:         model.StateSignedOut,
		Limits:        []model.RateLimit{},
		ObservedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	status = state.Status()
	if len(status.Accounts) != 0 || len(status.UnassignedUsers) != 1 ||
		status.UnassignedUsers[0].CodexVersion != "0.150.0" {
		t.Fatalf("signed-out user lost session version: %#v", status)
	}
}

func TestSignedOutClearsLastGoodAccountData(t *testing.T) {
	now := time.Now().UTC()
	state, _ := New([]Identity{{Username: "codex", UID: 123}}, time.Minute)
	state.now = func() time.Time { return now }
	if err := state.Apply(123, testSnapshot("codex", now, 8)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	signedOut := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      "codex",
		State:         model.StateSignedOut,
		Limits:        []model.RateLimit{},
		ObservedAt:    now,
	}
	if err := state.Apply(123, signedOut); err != nil {
		t.Fatal(err)
	}
	got := state.Status()
	if len(got.Accounts) != 0 || len(got.UnassignedUsers) != 1 ||
		got.UnassignedUsers[0].Username != "codex" || got.UnassignedUsers[0].State != model.StateSignedOut {
		t.Fatalf("signed-out snapshot retained account membership: %#v", got)
	}
}

func TestStatusAndSubscriptionsAreDefensiveCopies(t *testing.T) {
	now := time.Now().UTC()
	state, _ := New([]Identity{{Username: "codex", UID: 123}}, time.Minute)
	state.now = func() time.Time { return now }
	snapshot := testSnapshot("codex", now, 44)
	if err := state.Apply(123, snapshot); err != nil {
		t.Fatal(err)
	}

	status := state.Status()
	*status.Accounts[0].Account.Email = "mutated@example.com"
	*status.Accounts[0].MainUsage.ResetsAt = 1
	*status.Accounts[0].ResetCreditsAvailable = 99
	*status.Accounts[0].Limits[0].Primary.ResetsAt = 1
	fresh := state.Status()
	if *fresh.Accounts[0].Account.Email == "mutated@example.com" ||
		*fresh.Accounts[0].MainUsage.ResetsAt == 1 ||
		*fresh.Accounts[0].ResetCreditsAvailable == 99 ||
		*fresh.Accounts[0].Limits[0].Primary.ResetsAt == 1 {
		t.Fatal("caller mutated hub state through a returned snapshot")
	}

	updates, cancel := state.Subscribe()
	defer cancel()
	initial := <-updates
	if len(initial.Accounts) != 1 || initial.Revision == 0 {
		t.Fatalf("unexpected initial subscription state: %#v", initial)
	}
}

func TestFreshConsumerBeatsNewerStaleLastGoodSource(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	state, err := New([]Identity{
		{Username: "newer-stale", UID: 1},
		{Username: "older-fresh", UID: 2},
	}, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	email := "shared@example.com"
	stale := testSnapshot("newer-stale", now, 81)
	stale.Account.Email = &email
	if err := state.Apply(1, stale); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := state.Apply(1, model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      "newer-stale",
		State:         model.StateUnavailable,
		Limits:        []model.RateLimit{},
		ObservedAt:    now,
		ErrorCategory: model.ErrorCodexUnavailable,
	}); err != nil {
		t.Fatal(err)
	}
	fresh := testSnapshot("older-fresh", now.Add(-time.Minute), 23)
	fresh.Account.Email = &email
	if err := state.Apply(2, fresh); err != nil {
		t.Fatal(err)
	}
	status := state.Status().Accounts[0]
	if status.MainUsage == nil || status.MainUsage.UsedPercent != 23 || status.Stale || status.State != model.StateOK {
		t.Fatalf("fresh consumer was not canonical: %#v", status)
	}
}

func TestAccountSwitchClearsAccountScopedOptionalCaches(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	state, err := New([]Identity{{Username: "consumer", UID: 7}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	lifetime := int64(123456)
	first := testSnapshot("consumer", now, 10)
	first.LifetimeTokens = &lifetime
	first.LifetimeTokensRead = true
	first.RecentThreadsRead = true
	first.RecentThreads = []model.RecentThread{{ThreadID: "session-a", TaskName: "Private account A task"}}
	if err := state.Apply(7, first); err != nil {
		t.Fatal(err)
	}
	state.SetActivitySource(func() []ActivityRef {
		return []ActivityRef{{
			Username: "consumer", SessionID: "session-a", StartedAt: now, UpdatedAt: now, Running: true,
		}}
	})
	before := state.Status().Accounts[0]
	if before.LifetimeTokens == nil || *before.LifetimeTokens != lifetime ||
		len(before.ActiveChats) != 1 || before.ActiveChats[0].TaskName != "Private account A task" {
		t.Fatalf("initial optional cache = %#v", before)
	}

	now = now.Add(time.Second)
	second := testSnapshot("consumer", now, 20)
	secondEmail := "second@example.com"
	second.Account.Email = &secondEmail
	// Both optional reads failed, represented by false read flags.
	if err := state.Apply(7, second); err != nil {
		t.Fatal(err)
	}
	after := state.Status()
	if len(after.Accounts) != 1 || after.Accounts[0].Account == nil ||
		after.Accounts[0].Account.Email == nil || *after.Accounts[0].Account.Email != secondEmail {
		t.Fatalf("consumer did not move atomically: %#v", after)
	}
	if after.Accounts[0].LifetimeTokens != nil || len(after.Accounts[0].ActiveChats) != 0 {
		t.Fatalf("old account optional data leaked after switch: %#v", after.Accounts[0])
	}
}

func TestAnchorWrongAccountRoundTripClearsAccountScopedOptionalCaches(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	expectedEmail := "expected@example.com"
	state, err := New([]Identity{{Username: "anchor", UID: 8, ExpectedEmail: expectedEmail}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	expectedLifetime := int64(111)
	correct := testSnapshot("anchor", now, 10)
	correct.Account.Email = &expectedEmail
	correct.LifetimeTokensRead = true
	correct.LifetimeTokens = &expectedLifetime
	correct.RecentThreadsRead = true
	correct.RecentThreads = []model.RecentThread{{ThreadID: "expected-thread", TaskName: "Expected task"}}
	if err := state.Apply(8, correct); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Second)
	wrongEmail := "wrong@example.com"
	wrongLifetime := int64(222)
	wrong := testSnapshot("anchor", now, 20)
	wrong.Account.Email = &wrongEmail
	wrong.LifetimeTokensRead = true
	wrong.LifetimeTokens = &wrongLifetime
	wrong.RecentThreadsRead = true
	wrong.RecentThreads = []model.RecentThread{{ThreadID: "wrong-thread", TaskName: "Wrong task"}}
	if err := state.Apply(8, wrong); err != nil {
		t.Fatal(err)
	}
	if got := state.Status().Accounts[0]; got.AnchorHealth != model.AnchorHealthWrongAccount || got.LifetimeTokens != nil {
		t.Fatalf("wrong-account anchor exposed optional data: %#v", got)
	}

	now = now.Add(time.Second)
	back := testSnapshot("anchor", now, 30)
	back.Account.Email = &expectedEmail
	// Both optional reads fail on the first refresh back on the expected
	// account. The wrong account's values must already have been discarded.
	if err := state.Apply(8, back); err != nil {
		t.Fatal(err)
	}
	got := state.Status().Accounts[0]
	if got.AnchorHealth != model.AnchorHealthOK || got.LifetimeTokens != nil ||
		len(state.entries["anchor"].recentThreads) != 0 {
		t.Fatalf("wrong-account optional cache leaked after anchor returned: status=%#v entry=%#v", got, state.entries["anchor"])
	}
}

func TestAnchorSignOutClearsOptionalCachesBeforeSameAccountRelogin(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	email := "expected@example.com"
	state, err := New([]Identity{{Username: "anchor", UID: 9, ExpectedEmail: email}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	lifetime := int64(333)
	initial := testSnapshot("anchor", now, 10)
	initial.Account.Email = &email
	initial.LifetimeTokensRead = true
	initial.LifetimeTokens = &lifetime
	initial.RecentThreadsRead = true
	initial.RecentThreads = []model.RecentThread{{ThreadID: "before-signout", TaskName: "Before signout"}}
	if err := state.Apply(9, initial); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Second)
	if err := state.Apply(9, model.Snapshot{
		SchemaVersion: model.SchemaVersion, Username: "anchor", State: model.StateSignedOut,
		Limits: []model.RateLimit{}, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	relogin := testSnapshot("anchor", now, 20)
	relogin.Account.Email = &email
	// The first post-login optional calls fail.
	if err := state.Apply(9, relogin); err != nil {
		t.Fatal(err)
	}
	got := state.Status().Accounts[0]
	if got.AnchorHealth != model.AnchorHealthOK || got.LifetimeTokens != nil ||
		len(state.entries["anchor"].recentThreads) != 0 {
		t.Fatalf("pre-signout optional cache survived relogin: status=%#v entry=%#v", got, state.entries["anchor"])
	}
}

func TestCorrectFreshAnchorWinsAndReportsHealthPlanAndConflict(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	expected := "LocalUnitarity+1@Gmail.com"
	state, err := New([]Identity{
		{Username: "codex-dummy-1", UID: 11, ExpectedEmail: expected},
		{Username: "consumer", UID: 12},
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	consumer := testSnapshot("consumer", now, 9)
	consumer.Account.Email = stringPointer("localunitarity+1@gmail.com")
	if err := state.Apply(12, consumer); err != nil {
		t.Fatal(err)
	}
	anchor := testSnapshot("codex-dummy-1", now.Add(-time.Second), 72)
	anchor.Account.Email = stringPointer("LOCALUNITARITY+1@GMAIL.COM")
	anchor.Account.PlanType = "pro"
	if err := state.Apply(11, anchor); err != nil {
		t.Fatal(err)
	}
	got := state.Status()
	if len(got.Accounts) != 1 {
		t.Fatalf("aliases did not deduplicate by normalized identity: %#v", got)
	}
	status := got.Accounts[0]
	if status.AccountKey != model.AccountKey(expected) || status.AnchorHealth != model.AnchorHealthOK ||
		status.Account == nil || status.Account.Email == nil ||
		*status.Account.Email != "localunitarity+1@gmail.com" ||
		status.Account.PlanType != "pro" || status.MainUsage == nil || status.MainUsage.UsedPercent != 72 ||
		!status.SourceConflict {
		t.Fatalf("anchor aggregation = %#v", status)
	}
	if len(status.Users) != 2 || status.Users[0].Role != model.UserRoleAnchor ||
		status.Users[1].Role != model.UserRoleConsumer {
		t.Fatalf("account users = %#v", status.Users)
	}
}

func TestSourceConflictUsesQuotaToleranceBoundaries(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	baseline := testSnapshot("first", now, 40)
	baseline.Account.PlanType = " Pro "
	name := "Codex Weekly"
	limitPlan := " Pro "
	secondaryDuration := int64(60)
	secondaryReset := now.Add(time.Hour).Unix()
	balance := "12.00"
	reached := false
	reachedType := "none"
	baseline.Limits[0].Name = &name
	baseline.Limits[0].PlanType = &limitPlan
	baseline.Limits[0].Secondary = &model.Window{
		UsedPercent: 30, RemainingPercent: 70,
		WindowDurationMins: &secondaryDuration, ResetsAt: &secondaryReset,
	}
	baseline.Limits[0].Credits = &model.Credits{HasCredits: true, Balance: &balance}
	baseline.Limits[0].IndividualLimit = &model.IndividualLimit{
		Limit: "1000", Used: "400", RemainingPercent: 60, ResetsAt: secondaryReset,
	}
	baseline.Limits[0].SpendControlReached = &reached
	baseline.Limits[0].ReachedType = &reachedType

	within := cloneSnapshot(baseline)
	within.Account.PlanType = "pro"
	withinName := "  CODEX   weekly "
	withinPlan := "pro"
	within.Limits[0].Name = &withinName
	within.Limits[0].PlanType = &withinPlan
	within.MainUsage.UsedPercent += quotaPercentTolerance
	within.MainUsage.RemainingPercent -= quotaPercentTolerance
	*within.MainUsage.ResetsAt += quotaResetTolerance
	within.Limits[0].Primary.UsedPercent -= quotaPercentTolerance
	within.Limits[0].Primary.RemainingPercent += quotaPercentTolerance
	*within.Limits[0].Primary.ResetsAt -= quotaResetTolerance
	within.Limits[0].Secondary.UsedPercent += quotaPercentTolerance
	within.Limits[0].Secondary.RemainingPercent -= quotaPercentTolerance
	*within.Limits[0].Secondary.ResetsAt += quotaResetTolerance
	within.Limits[0].IndividualLimit.RemainingPercent -= quotaPercentTolerance
	within.Limits[0].IndividualLimit.ResetsAt -= quotaResetTolerance
	within.Limits[0].IndividualLimit.Used = "405"
	if snapshotsConflict([]model.Snapshot{baseline, within}) {
		t.Fatal("differences exactly at percentage/reset tolerance were marked conflicting")
	}

	tests := []struct {
		name   string
		mutate func(*model.Snapshot)
	}{
		{
			name: "main percentage",
			mutate: func(snapshot *model.Snapshot) {
				snapshot.MainUsage.UsedPercent += quotaPercentTolerance + 1
				snapshot.MainUsage.RemainingPercent -= quotaPercentTolerance + 1
			},
		},
		{
			name: "primary reset",
			mutate: func(snapshot *model.Snapshot) {
				*snapshot.Limits[0].Primary.ResetsAt += quotaResetTolerance + 1
			},
		},
		{
			name: "secondary percentage",
			mutate: func(snapshot *model.Snapshot) {
				snapshot.Limits[0].Secondary.UsedPercent += quotaPercentTolerance + 1
				snapshot.Limits[0].Secondary.RemainingPercent -= quotaPercentTolerance + 1
			},
		},
		{
			name: "individual reset",
			mutate: func(snapshot *model.Snapshot) {
				snapshot.Limits[0].IndividualLimit.ResetsAt += quotaResetTolerance + 1
			},
		},
		{
			name: "individual shape",
			mutate: func(snapshot *model.Snapshot) {
				snapshot.Limits[0].IndividualLimit.Limit = "2000"
			},
		},
		{
			name: "account plan",
			mutate: func(snapshot *model.Snapshot) {
				snapshot.Account.PlanType = "plus"
			},
		},
		{
			name: "main duration",
			mutate: func(snapshot *model.Snapshot) {
				*snapshot.MainUsage.WindowDurationMins++
			},
		},
		{
			name: "limit shape",
			mutate: func(snapshot *model.Snapshot) {
				snapshot.Limits[0].Secondary = nil
			},
		},
		{
			name: "limit ID",
			mutate: func(snapshot *model.Snapshot) {
				snapshot.Limits[0].ID = "different"
			},
		},
		{
			name: "reset credits",
			mutate: func(snapshot *model.Snapshot) {
				*snapshot.ResetCreditsAvailable++
			},
		},
		{
			name: "credit balance",
			mutate: func(snapshot *model.Snapshot) {
				changed := "13.00"
				snapshot.Limits[0].Credits.Balance = &changed
			},
		},
		{
			name: "reached flag",
			mutate: func(snapshot *model.Snapshot) {
				*snapshot.Limits[0].SpendControlReached = true
			},
		},
		{
			name: "reached type",
			mutate: func(snapshot *model.Snapshot) {
				changed := "hard_limit"
				snapshot.Limits[0].ReachedType = &changed
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneSnapshot(baseline)
			test.mutate(&changed)
			if !snapshotsConflict([]model.Snapshot{baseline, changed}) {
				t.Fatal("material quota disagreement was not marked conflicting")
			}
		})
	}
}

func TestWrongAndSignedOutAnchorHealth(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	state, err := New([]Identity{{
		Username: "codex-dummy-0", UID: 19, ExpectedEmail: "expected@example.com",
	}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	wrong := testSnapshot("codex-dummy-0", now, 5)
	if err := state.Apply(19, wrong); err != nil {
		t.Fatal(err)
	}
	if got := state.Status().Accounts[0]; got.AnchorHealth != model.AnchorHealthWrongAccount ||
		got.Account == nil || got.Account.Email == nil || *got.Account.Email != "expected@example.com" {
		t.Fatalf("wrong-account anchor = %#v", got)
	}
	now = now.Add(time.Second)
	if err := state.Apply(19, model.Snapshot{
		SchemaVersion: model.SchemaVersion, Username: "codex-dummy-0", State: model.StateSignedOut,
		Limits: []model.RateLimit{}, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if got := state.Status().Accounts[0]; got.AnchorHealth != model.AnchorHealthSignedOut ||
		got.State != model.StateSignedOut {
		t.Fatalf("signed-out anchor = %#v", got)
	}
}

func TestActivityUsesConsumerMembershipHidesAnchorsAndNeverPublishesIDs(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	email := "shared@example.com"
	state, err := New([]Identity{
		{Username: "anchor", UID: 31, ExpectedEmail: email},
		{Username: "consumer", UID: 32},
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	longName := "Resolve\n" + strings.Repeat("é", 100)
	for uid, username := range map[uint32]string{31: "anchor", 32: "consumer"} {
		snapshot := testSnapshot(username, now, int(uid))
		snapshot.Account.Email = &email
		snapshot.RecentThreadsRead = true
		snapshot.RecentThreads = []model.RecentThread{{
			ThreadID: "private-session-" + username,
			TaskName: model.SanitizeTaskName(longName),
		}}
		snapshot.RuntimeThreadsRead = true
		snapshot.RuntimeThreads = []model.RuntimeThread{{
			ThreadID:  "private-session-" + username,
			TaskName:  model.SanitizeTaskName(longName),
			CreatedAt: now.Add(-time.Hour).Unix(),
			UpdatedAt: now.Add(-time.Minute).Unix(),
			Running:   false,
		}}
		if err := state.Apply(uid, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	state.SetActivitySource(func() []ActivityRef {
		return []ActivityRef{
			{Username: "anchor", SessionID: "private-session-anchor", StartedAt: now, UpdatedAt: now, Running: true},
			{Username: "consumer", SessionID: "private-session-consumer", StartedAt: now, UpdatedAt: now, Running: true},
		}
	})
	status := state.Status()
	if len(status.Accounts) != 1 || len(status.Accounts[0].ActiveChats) != 1 ||
		status.Accounts[0].ActiveChats[0].Username != "consumer" ||
		!status.Accounts[0].ActiveChats[0].Running ||
		len(status.Accounts[0].ActiveChats[0].TaskName) > model.MaxTaskNameBytes ||
		strings.ContainsAny(status.Accounts[0].ActiveChats[0].TaskName, "\r\n\t") {
		t.Fatalf("activity projection = %#v", status)
	}
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{
		[]byte("private-session-anchor"),
		[]byte("private-session-consumer"),
		[]byte("threadId"),
		[]byte("sessionId"),
		[]byte("turnId"),
	} {
		if bytes.Contains(payload, forbidden) {
			t.Fatalf("public status exposed activity identifier %q: %s", forbidden, payload)
		}
	}
}

func TestRuntimeThreadsProjectOnlyRunningWithoutPublishingIDs(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	state, err := New([]Identity{{Username: "consumer", UID: 33}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	snapshot := testSnapshot("consumer", now, 17)
	snapshot.RuntimeThreadsRead = true
	snapshot.RuntimeThreads = []model.RuntimeThread{
		{
			ThreadID: "private-runtime-active", TaskName: "Running dashboard repair",
			CreatedAt: now.Add(-2 * time.Hour).Unix(), UpdatedAt: now.Add(-time.Minute).Unix(), Running: true,
		},
		{
			ThreadID: "private-runtime-idle", TaskName: "Idle dashboard review",
			CreatedAt: now.Add(-time.Hour).Unix(), UpdatedAt: now.Add(-2 * time.Minute).Unix(), Running: false,
		},
	}
	if err := state.Apply(33, snapshot); err != nil {
		t.Fatal(err)
	}

	status := state.Status()
	if len(status.Accounts) != 1 || len(status.Accounts[0].ActiveChats) != 1 {
		t.Fatalf("runtime activity = %#v", status)
	}
	active := status.Accounts[0].ActiveChats[0]
	if active.TaskName != "Running dashboard repair" || active.Username != "consumer" || !active.Running ||
		!active.StartedAt.Equal(now.Add(-2*time.Hour)) || !active.UpdatedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("active runtime chat = %#v", active)
	}
	stored := state.entries["consumer"]
	if len(stored.runtimeThreads) != 2 || len(stored.current.RuntimeThreads) != 0 || stored.current.RuntimeThreadsRead ||
		stored.lastGood == nil || len(stored.lastGood.RuntimeThreads) != 0 || stored.lastGood.RuntimeThreadsRead {
		t.Fatalf("runtime IDs were not isolated in the private map: %#v", stored)
	}
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-runtime-active", "private-runtime-idle", "runtimeThreads", "threadId"} {
		if bytes.Contains(payload, []byte(forbidden)) {
			t.Fatalf("public status exposed private runtime metadata %q: %s", forbidden, payload)
		}
	}
	now = now.Add(time.Second)
	replacement := testSnapshot("consumer", now, 18)
	replacement.RuntimeThreadsRead = true
	replacement.RuntimeThreads = []model.RuntimeThread{{
		ThreadID: "private-runtime-replacement", TaskName: "Replacement task",
		CreatedAt: now.Add(-time.Hour).Unix(), UpdatedAt: now.Unix(), Running: true,
	}}
	if err := state.Apply(33, replacement); err != nil {
		t.Fatal(err)
	}
	if got := state.Status().Accounts[0].ActiveChats; len(got) != 1 || got[0].TaskName != "Replacement task" {
		t.Fatalf("runtime observation was not replaced atomically: %#v", got)
	}
	if stored := state.entries["consumer"].runtimeThreads; len(stored) != 1 {
		t.Fatalf("private runtime map was not replaced: %#v", stored)
	}

	// If collector updates cease altogether, stale runtime inventory is hidden
	// at the same freshness boundary as its owning consumer snapshot.
	now = now.Add(time.Minute + time.Second)
	if got := state.Status().Accounts[0].ActiveChats; len(got) != 0 {
		t.Fatalf("stale runtime activity remained visible: %#v", got)
	}
}

func TestRuntimeAndHookActivityMergeByPrivateSession(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	state, err := New([]Identity{{Username: "consumer", UID: 34}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	snapshot := testSnapshot("consumer", now, 18)
	snapshot.RecentThreadsRead = true
	snapshot.RecentThreads = []model.RecentThread{
		{ThreadID: "shared-private-id", TaskName: "Older recent name"},
		{ThreadID: "hook-recent-id", TaskName: "Recent hook title"},
	}
	snapshot.RuntimeThreadsRead = true
	snapshot.RuntimeThreads = []model.RuntimeThread{{
		ThreadID: "shared-private-id", TaskName: "Live runtime name",
		CreatedAt: now.Add(-30 * time.Minute).Unix(), UpdatedAt: now.Add(-10 * time.Minute).Unix(), Running: false,
	}}
	if err := state.Apply(34, snapshot); err != nil {
		t.Fatal(err)
	}
	state.SetActivitySource(func() []ActivityRef {
		return []ActivityRef{
			{Username: "consumer", SessionID: "shared-private-id", StartedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-5 * time.Minute), Running: true},
			{Username: "consumer", SessionID: "shared-private-id", StartedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Minute), Running: false},
			{Username: "consumer", SessionID: "hook-recent-id", StartedAt: now.Add(-20 * time.Minute), UpdatedAt: now.Add(-3 * time.Minute), Running: false},
			{Username: "consumer", SessionID: "hook-fallback-id", StartedAt: now.Add(-10 * time.Minute), UpdatedAt: now.Add(-time.Minute), Running: false},
		}
	})

	chats := state.Status().Accounts[0].ActiveChats
	if len(chats) != 1 {
		t.Fatalf("merged chats = %#v", chats)
	}
	byName := make(map[string]model.ActiveChat, len(chats))
	for _, chat := range chats {
		byName[chat.TaskName] = chat
	}
	merged, ok := byName["Live runtime name"]
	if !ok || !merged.Running || !merged.StartedAt.Equal(now.Add(-2*time.Hour)) ||
		!merged.UpdatedAt.Equal(now.Add(-2*time.Minute)) {
		t.Fatalf("runtime/hook merge = %#v", merged)
	}
	if _, ok := byName["Recent hook title"]; ok {
		t.Fatalf("idle hook chat was published: %#v", chats)
	}
	if _, ok := byName["Active Codex task"]; ok {
		t.Fatalf("idle fallback chat was published: %#v", chats)
	}
}

func TestRuntimeDedupeKeyIncludesUsername(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	state, err := New([]Identity{
		{Username: "alice", UID: 36},
		{Username: "bob", UID: 37},
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	email := "shared@example.com"
	for uid, username := range map[uint32]string{36: "alice", 37: "bob"} {
		snapshot := testSnapshot(username, now, 21)
		snapshot.Account.Email = &email
		snapshot.RuntimeThreadsRead = true
		snapshot.RuntimeThreads = []model.RuntimeThread{{
			ThreadID: "same-private-id", TaskName: "Task for " + username,
			CreatedAt: now.Add(-time.Hour).Unix(), UpdatedAt: now.Unix(), Running: true,
		}}
		if err := state.Apply(uid, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	chats := state.Status().Accounts[0].ActiveChats
	if len(chats) != 2 || chats[0].Username == chats[1].Username {
		t.Fatalf("same private ID from distinct users was collapsed: %#v", chats)
	}
}

func TestRuntimeActivityRetentionAndClearing(t *testing.T) {
	for _, test := range []struct {
		name         string
		nextSnapshot func(username string, at time.Time) model.Snapshot
		wantAccounts int
		wantRuntime  int
		wantChats    int
		wantKnown    bool
	}{
		{
			name: "optional read failure",
			nextSnapshot: func(username string, at time.Time) model.Snapshot {
				return testSnapshot(username, at, 20)
			},
			wantAccounts: 1, wantRuntime: 1, wantChats: 1, wantKnown: true,
		},
		{
			name: "successful empty read",
			nextSnapshot: func(username string, at time.Time) model.Snapshot {
				snapshot := testSnapshot(username, at, 20)
				snapshot.RuntimeThreadsRead = true
				return snapshot
			},
			wantAccounts: 1, wantKnown: true,
		},
		{
			name: "unavailable collector",
			nextSnapshot: func(username string, at time.Time) model.Snapshot {
				return model.Snapshot{
					SchemaVersion: model.SchemaVersion, Username: username, State: model.StateUnavailable,
					Limits: []model.RateLimit{}, ObservedAt: at, ErrorCategory: model.ErrorCodexUnavailable,
				}
			},
			wantAccounts: 1,
		},
		{
			name: "signed out",
			nextSnapshot: func(username string, at time.Time) model.Snapshot {
				return model.Snapshot{
					SchemaVersion: model.SchemaVersion, Username: username, State: model.StateSignedOut,
					Limits: []model.RateLimit{}, ObservedAt: at,
				}
			},
			wantAccounts: 0,
		},
		{
			name: "account switch",
			nextSnapshot: func(username string, at time.Time) model.Snapshot {
				snapshot := testSnapshot(username, at, 20)
				email := "switched@example.com"
				snapshot.Account.Email = &email
				return snapshot
			},
			wantAccounts: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			state, err := New([]Identity{{Username: "consumer", UID: 35}}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			state.now = func() time.Time { return now }
			initial := testSnapshot("consumer", now, 19)
			initial.RuntimeThreadsRead = true
			initial.RuntimeThreads = []model.RuntimeThread{{
				ThreadID: "must-not-linger", TaskName: "Old runtime task",
				CreatedAt: now.Add(-time.Hour).Unix(), UpdatedAt: now.Unix(), Running: true,
			}}
			if err := state.Apply(35, initial); err != nil {
				t.Fatal(err)
			}
			if got := state.Status().Accounts[0].ActiveChats; len(got) != 1 {
				t.Fatalf("initial runtime activity = %#v", got)
			}

			now = now.Add(time.Second)
			if err := state.Apply(35, test.nextSnapshot("consumer", now)); err != nil {
				t.Fatal(err)
			}
			status := state.Status()
			if len(state.entries["consumer"].runtimeThreads) != test.wantRuntime {
				t.Fatalf("private runtime cache length = %d, want %d: %#v",
					len(state.entries["consumer"].runtimeThreads), test.wantRuntime,
					state.entries["consumer"].runtimeThreads)
			}
			if len(status.Accounts) != test.wantAccounts {
				t.Fatalf("accounts after transition = %#v", status.Accounts)
			}
			if test.wantAccounts == 0 {
				if len(status.UnassignedUsers) != 1 || status.UnassignedUsers[0].ActiveChatsKnown {
					t.Fatalf("signed-out coverage = %#v", status.UnassignedUsers)
				}
				return
			}
			if len(status.Accounts[0].ActiveChats) != test.wantChats ||
				len(status.Accounts[0].Users) != 1 ||
				status.Accounts[0].Users[0].ActiveChatsKnown != test.wantKnown {
				t.Fatalf("runtime transition status = %#v", status.Accounts[0])
			}
		})
	}
}

func TestRuntimeInventoryGraceExpiresIndependentlyOfCollectorFreshness(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	state, err := New([]Identity{{Username: "consumer", UID: 135}}, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	initial := testSnapshot("consumer", now, 19)
	initial.RuntimeThreadsRead = true
	initial.RuntimeThreads = []model.RuntimeThread{{
		ThreadID: "private-runtime", TaskName: "Running task", Running: true,
	}}
	if err := state.Apply(135, initial); err != nil {
		t.Fatal(err)
	}

	now = now.Add(runtimeInventoryGrace)
	status := state.Status().Accounts[0]
	if len(status.ActiveChats) != 1 || !status.Users[0].ActiveChatsKnown {
		t.Fatalf("runtime inventory expired at the inclusive boundary: %#v", status)
	}

	now = now.Add(time.Nanosecond)
	status = state.Status().Accounts[0]
	if len(status.ActiveChats) != 0 || status.Users[0].ActiveChatsKnown {
		t.Fatalf("expired runtime inventory remained authoritative: %#v", status)
	}

	// A fresh quota observation with another optional runtime failure also
	// releases the expired private cache rather than extending its lifetime.
	failedRead := testSnapshot("consumer", now, 20)
	if err := state.Apply(135, failedRead); err != nil {
		t.Fatal(err)
	}
	stored := state.entries["consumer"]
	if len(stored.runtimeThreads) != 0 || !stored.runtimeInventoryAt.IsZero() {
		t.Fatalf("expired runtime cache survived a failed refresh: %#v", stored)
	}
}

func TestRuntimeIDsAreQuarantinedAcrossAccountSwitchUntilTheyDisappear(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	state, err := New([]Identity{{Username: "consumer", UID: 38}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	runtimeThread := func(id, name string) model.RuntimeThread {
		return model.RuntimeThread{
			ThreadID: id, TaskName: name, CreatedAt: now.Add(-time.Hour).Unix(),
			UpdatedAt: now.Unix(), Running: true,
		}
	}
	apply := func(email string, threads ...model.RuntimeThread) {
		t.Helper()
		snapshot := testSnapshot("consumer", now, 20)
		snapshot.Account.Email = &email
		snapshot.RuntimeThreadsRead = true
		snapshot.RuntimeThreads = threads
		if err := state.Apply(38, snapshot); err != nil {
			t.Fatal(err)
		}
	}

	apply("first@example.com", runtimeThread("old-session", "Old account task"))
	if got := state.Status().Accounts[0].ActiveChats; len(got) != 1 {
		t.Fatalf("initial runtime activity = %#v", got)
	}
	activities := []ActivityRef{{
		Username: "consumer", SessionID: "hook-only-old-session", StartedAt: now.Add(-time.Hour),
		UpdatedAt: now, Running: true,
	}}
	state.SetActivitySource(func() []ActivityRef { return activities })

	// Every ID loaded at the exact account boundary is ambiguous, including
	// one which had not appeared in the prior collector observation. A
	// hook-only session already running at the boundary is captured too.
	now = now.Add(time.Second)
	apply("second@example.com",
		runtimeThread("old-session", "Old account task"),
		runtimeThread("boundary-session", "Boundary task"),
	)
	if got := state.Status().Accounts[0].ActiveChats; len(got) != 0 {
		t.Fatalf("boundary runtime IDs were reassigned to the new account: %#v", got)
	}
	if len(state.entries["consumer"].runtimeQuarantine) != 3 {
		t.Fatalf("boundary quarantine = %#v", state.entries["consumer"].runtimeQuarantine)
	}

	// A genuinely new ID is visible immediately; an ID that remains loaded
	// across observations stays hidden. The missing boundary ID is retired.
	now = now.Add(time.Second)
	apply("second@example.com",
		runtimeThread("old-session", "Old account task"),
		runtimeThread("new-session", "New account task"),
	)
	activities = append(activities, ActivityRef{
		Username: "consumer", SessionID: "hook-only-new-session", StartedAt: now,
		UpdatedAt: now, Running: true,
	})
	got := state.Status().Accounts[0].ActiveChats
	if len(got) != 2 {
		t.Fatalf("post-switch new runtime activity = %#v", got)
	}
	if _, present := state.entries["consumer"].runtimeQuarantine["boundary-session"]; present {
		t.Fatal("an absent runtime ID did not leave quarantine")
	}
	if _, present := state.entries["consumer"].runtimeQuarantine["hook-only-old-session"]; !present {
		t.Fatal("a still-tracked hook-only boundary ID left quarantine")
	}

	// Once a complete observation proves disappearance, the old ID may be
	// displayed if it appears again in a later observation.
	activities = nil
	now = now.Add(time.Second)
	apply("second@example.com", runtimeThread("new-session", "New account task"))
	if len(state.entries["consumer"].runtimeQuarantine) != 0 {
		t.Fatalf("IDs absent from runtime and hook state remained quarantined: %#v", state.entries["consumer"].runtimeQuarantine)
	}
	now = now.Add(time.Second)
	apply("second@example.com",
		runtimeThread("old-session", "Reopened task"),
		runtimeThread("new-session", "New account task"),
	)
	got = state.Status().Accounts[0].ActiveChats
	if len(got) != 2 {
		t.Fatalf("disappeared runtime ID could not reappear: %#v", got)
	}
	payload, err := json.Marshal(state.Status())
	if err != nil {
		t.Fatal(err)
	}
	for _, privateID := range []string{
		"old-session", "boundary-session", "new-session", "hook-only-old-session", "hook-only-new-session",
	} {
		if bytes.Contains(payload, []byte(privateID)) {
			t.Fatalf("public JSON exposed quarantined runtime ID %q: %s", privateID, payload)
		}
	}
}

func TestRuntimeQuarantineWaitsForCompleteReadAndSurvivesFailures(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	state, err := New([]Identity{{Username: "consumer", UID: 39}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	first := testSnapshot("consumer", now, 10)
	first.RuntimeThreadsRead = true
	first.RuntimeThreads = []model.RuntimeThread{{ThreadID: "carried-session", TaskName: "Carried task", Running: true}}
	if err := state.Apply(39, first); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Second)
	switchWithoutRuntime := testSnapshot("consumer", now, 11)
	secondEmail := "second@example.com"
	switchWithoutRuntime.Account.Email = &secondEmail
	if err := state.Apply(39, switchWithoutRuntime); err != nil {
		t.Fatal(err)
	}
	if stored := state.entries["consumer"]; !stored.runtimeQuarantinePending || len(stored.runtimeThreads) != 0 {
		t.Fatalf("failed boundary read did not establish pending quarantine: %#v", stored)
	}
	state.SetActivitySource(func() []ActivityRef {
		return []ActivityRef{{
			Username: "consumer", SessionID: "carried-session", StartedAt: now.Add(-time.Hour),
			UpdatedAt: now, Running: true,
		}}
	})
	if got := state.Status().Accounts[0].ActiveChats; len(got) != 0 {
		t.Fatalf("hook activity was exposed while boundary quarantine was pending: %#v", got)
	}

	// A transient collector failure neither changes membership nor proves
	// absence of any private runtime ID.
	now = now.Add(time.Second)
	if err := state.Apply(39, model.Snapshot{
		SchemaVersion: model.SchemaVersion, Username: "consumer", State: model.StateUnavailable,
		Limits: []model.RateLimit{}, ObservedAt: now, ErrorCategory: model.ErrorCodexUnavailable,
	}); err != nil {
		t.Fatal(err)
	}
	status := state.Status()
	if len(status.Accounts) != 1 || len(status.Accounts[0].Users) != 1 ||
		status.Accounts[0].Users[0].Username != "consumer" || !state.entries["consumer"].runtimeQuarantinePending {
		t.Fatalf("transient failure broke membership/quarantine: status=%#v entry=%#v", status, state.entries["consumer"])
	}

	// The first later complete observation is entirely quarantined. A later
	// new ID is visible while the still-present carried ID remains hidden.
	now = now.Add(time.Second)
	firstComplete := testSnapshot("consumer", now, 12)
	firstComplete.Account.Email = &secondEmail
	firstComplete.RuntimeThreadsRead = true
	firstComplete.RuntimeThreads = []model.RuntimeThread{{ThreadID: "carried-session", TaskName: "Carried task", Running: true}}
	if err := state.Apply(39, firstComplete); err != nil {
		t.Fatal(err)
	}
	firstStatus := state.Status().Accounts[0]
	if len(firstStatus.ActiveChats) != 0 || firstStatus.Users[0].ActiveChatsKnown {
		t.Fatalf("first post-boundary observation was treated as authoritative: %#v", firstStatus)
	}
	now = now.Add(time.Second)
	next := testSnapshot("consumer", now, 13)
	next.Account.Email = &secondEmail
	next.RuntimeThreadsRead = true
	next.RuntimeThreads = []model.RuntimeThread{
		{ThreadID: "carried-session", TaskName: "Carried task", Running: true},
		{ThreadID: "new-session", TaskName: "New task", Running: true},
	}
	if err := state.Apply(39, next); err != nil {
		t.Fatal(err)
	}
	nextStatus := state.Status().Accounts[0]
	if len(nextStatus.ActiveChats) != 1 || nextStatus.ActiveChats[0].TaskName != "New task" ||
		nextStatus.Users[0].ActiveChatsKnown {
		t.Fatalf("quarantined inventory coverage = %#v", nextStatus)
	}

	// An optional read failure briefly retains the last successful visible
	// inventory and cannot retire an existing quarantine. Explicit sign-out
	// still clears that inventory and unassigns the user.
	now = now.Add(time.Second)
	readFailure := testSnapshot("consumer", now, 14)
	readFailure.Account.Email = &secondEmail
	if err := state.Apply(39, readFailure); err != nil {
		t.Fatal(err)
	}
	if len(state.entries["consumer"].runtimeThreads) != 1 ||
		len(state.entries["consumer"].runtimeQuarantine) != 1 ||
		state.Status().Accounts[0].Users[0].ActiveChatsKnown {
		t.Fatalf("optional failure mishandled private runtime state: %#v", state.entries["consumer"])
	}
	now = now.Add(time.Second)
	if err := state.Apply(39, model.Snapshot{
		SchemaVersion: model.SchemaVersion, Username: "consumer", State: model.StateSignedOut,
		Limits: []model.RateLimit{}, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if got := state.Status(); len(got.Accounts) != 0 || len(got.UnassignedUsers) != 1 ||
		got.UnassignedUsers[0].ActiveChatsKnown || len(state.entries["consumer"].runtimeThreads) != 0 ||
		!state.entries["consumer"].runtimeInventoryAt.IsZero() ||
		len(state.entries["consumer"].runtimeQuarantine) != 1 {
		t.Fatalf("sign-out broke membership/quarantine semantics: %#v", got)
	}
}

func TestSameAccountRefreshAndReloginDoNotCreateRuntimeQuarantine(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	state, err := New([]Identity{{Username: "consumer", UID: 40}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	applyOK := func() {
		t.Helper()
		snapshot := testSnapshot("consumer", now, 20)
		snapshot.RuntimeThreadsRead = true
		snapshot.RuntimeThreads = []model.RuntimeThread{{ThreadID: "same-session", TaskName: "Same account task", Running: true}}
		if err := state.Apply(40, snapshot); err != nil {
			t.Fatal(err)
		}
		if got := state.Status().Accounts[0].ActiveChats; len(got) != 1 {
			t.Fatalf("same-account runtime activity = %#v", got)
		}
		if stored := state.entries["consumer"]; stored.runtimeQuarantinePending || len(stored.runtimeQuarantine) != 0 {
			t.Fatalf("same account was needlessly quarantined: %#v", stored)
		}
	}
	applyOK()
	now = now.Add(time.Second)
	applyOK()
	now = now.Add(time.Second)
	if err := state.Apply(40, model.Snapshot{
		SchemaVersion: model.SchemaVersion, Username: "consumer", State: model.StateSignedOut,
		Limits: []model.RateLimit{}, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	applyOK()
}

func TestCanonicalSourceSwitchRebaselinesHistory(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	email := "shared@example.com"
	state, err := New([]Identity{
		{Username: "anchor", UID: 41, ExpectedEmail: email},
		{Username: "consumer", UID: 42},
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	tracker, err := usagehistory.Open("", 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	state.SetHistory(tracker)

	resetAt := now.Add(6 * 24 * time.Hour).Unix()
	consumer := testSnapshot("consumer", now, 50)
	consumer.Account.Email = &email
	consumer.MainUsage.ResetsAt = &resetAt
	if err := state.Apply(42, consumer); err != nil {
		t.Fatal(err)
	}
	assertHistory := func(wantUsed, wantAdjustments int) {
		t.Helper()
		got := tracker.Snapshot()
		if len(got.Accounts) != 1 || got.Accounts[0].Active == nil ||
			got.Accounts[0].Active.UsedPercent != wantUsed || len(got.Accounts[0].Events) != 0 ||
			len(got.Accounts[0].Adjustments) != wantAdjustments {
			t.Fatalf("history = %#v; want used=%d adjustments=%d", got, wantUsed, wantAdjustments)
		}
	}
	assertHistory(50, 0)

	// A correctly mapped anchor takes canonical-source priority. Its lower
	// value is a cross-collector discrepancy, not an account adjustment.
	now = now.Add(time.Second)
	anchor := testSnapshot("anchor", now, 40)
	anchor.Account.Email = &email
	anchor.MainUsage.ResetsAt = &resetAt
	if err := state.Apply(41, anchor); err != nil {
		t.Fatal(err)
	}
	assertHistory(40, 0)

	// Once the anchor remains the canonical source, a later decrease is a
	// genuine stable-source observation and follows normal detection.
	now = now.Add(time.Second)
	anchor = testSnapshot("anchor", now, 30)
	anchor.Account.Email = &email
	anchor.MainUsage.ResetsAt = &resetAt
	if err := state.Apply(41, anchor); err != nil {
		t.Fatal(err)
	}
	assertHistory(30, 1)
}

func TestHistorySourceStaysStickyAcrossOutOfPhaseConsumers(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	email := "shared@example.com"
	state, err := New([]Identity{
		{Username: "alpha", UID: 43},
		{Username: "beta", UID: 44},
	}, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	tracker, err := usagehistory.Open("", 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	state.SetHistory(tracker)
	resetAt := now.Add(6 * 24 * time.Hour).Unix()
	apply := func(uid uint32, username string, used int) {
		t.Helper()
		snapshot := testSnapshot(username, now, used)
		snapshot.Account.Email = &email
		snapshot.MainUsage.ResetsAt = &resetAt
		if err := state.Apply(uid, snapshot); err != nil {
			t.Fatal(err)
		}
	}

	apply(43, "alpha", 40)
	now = now.Add(10 * time.Second)
	apply(44, "beta", 38)
	if got := tracker.Snapshot().Accounts[0]; got.Active == nil || got.Active.UsedPercent != 40 ||
		len(got.Adjustments) != 0 {
		t.Fatalf("newer duplicate collector replaced sticky history source: %#v", got)
	}

	now = now.Add(10 * time.Second)
	apply(43, "alpha", 41)
	now = now.Add(10 * time.Second)
	apply(44, "beta", 39)
	got := tracker.Snapshot().Accounts[0]
	if got.Active == nil || got.Active.UsedPercent != 41 || len(got.Adjustments) != 0 ||
		state.historyObserved[model.AccountKey(email)].source.username != "alpha" {
		t.Fatalf("out-of-phase polling churned account history: %#v source=%#v", got,
			state.historyObserved[model.AccountKey(email)].source)
	}
}

func TestBriefAnchorFailurePreservesHistoryContinuity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	email := "anchor@example.com"
	state, err := New([]Identity{{Username: "anchor", UID: 45, ExpectedEmail: email}}, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	tracker, err := usagehistory.Open("", 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	state.SetHistory(tracker)
	resetAt := now.Add(6 * 24 * time.Hour).Unix()
	initial := testSnapshot("anchor", now, 50)
	initial.Account.Email = &email
	initial.MainUsage.ResetsAt = &resetAt
	if err := state.Apply(45, initial); err != nil {
		t.Fatal(err)
	}
	accountKey := model.AccountKey(email)
	beforeSource := state.historyObserved[accountKey].source

	now = now.Add(30 * time.Second)
	if err := state.Apply(45, model.Snapshot{
		SchemaVersion: model.SchemaVersion, Username: "anchor", State: model.StateUnavailable,
		Limits: []model.RateLimit{}, ObservedAt: now, ErrorCategory: model.ErrorRateLimitRead,
	}); err != nil {
		t.Fatal(err)
	}
	if afterFailure := state.historyObserved[accountKey].source; afterFailure != beforeSource {
		t.Fatalf("brief failure discarded source continuity: before=%#v after=%#v", beforeSource, afterFailure)
	}

	now = now.Add(30 * time.Second)
	recovered := testSnapshot("anchor", now, 40)
	recovered.Account.Email = &email
	recovered.MainUsage.ResetsAt = &resetAt
	if err := state.Apply(45, recovered); err != nil {
		t.Fatal(err)
	}
	got := tracker.Snapshot().Accounts[0]
	if got.Active == nil || got.Active.UsedPercent != 40 || len(got.Adjustments) != 1 ||
		state.historyObserved[accountKey].source != beforeSource {
		t.Fatalf("anchor recovery was rebased instead of continued: %#v", got)
	}
}

func TestHistorySourceFallsBackOnceAfterStalenessAndReturnsToAnchorOnce(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	email := "anchor@example.com"
	state, err := New([]Identity{
		{Username: "anchor", UID: 46, ExpectedEmail: email},
		{Username: "consumer", UID: 47},
	}, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	tracker, err := usagehistory.Open("", 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	state.SetHistory(tracker)
	resetAt := now.Add(6 * 24 * time.Hour).Unix()
	applyOK := func(uid uint32, username string, used int) {
		t.Helper()
		snapshot := testSnapshot(username, now, used)
		snapshot.Account.Email = &email
		snapshot.MainUsage.ResetsAt = &resetAt
		if err := state.Apply(uid, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	applyOK(47, "consumer", 50)
	applyOK(46, "anchor", 50)
	accountKey := model.AccountKey(email)
	if source := state.historyObserved[accountKey].source.username; source != "anchor" {
		t.Fatalf("history did not upgrade to anchor: %q", source)
	}

	now = now.Add(10 * time.Second)
	if err := state.Apply(46, model.Snapshot{
		SchemaVersion: model.SchemaVersion, Username: "anchor", State: model.StateUnavailable,
		Limits: []model.RateLimit{}, ObservedAt: now, ErrorCategory: model.ErrorRateLimitRead,
	}); err != nil {
		t.Fatal(err)
	}
	applyOK(47, "consumer", 47)
	if source := state.historyObserved[accountKey].source.username; source != "anchor" {
		t.Fatalf("brief failure switched history source to %q", source)
	}

	now = now.Add(91 * time.Second)
	applyOK(47, "consumer", 45)
	if source := state.historyObserved[accountKey].source.username; source != "consumer" {
		t.Fatalf("stale anchor did not fall back once: %q", source)
	}
	now = now.Add(10 * time.Second)
	applyOK(46, "anchor", 44)
	if source := state.historyObserved[accountKey].source.username; source != "anchor" {
		t.Fatalf("recovered anchor did not regain history priority: %q", source)
	}
	now = now.Add(time.Second)
	applyOK(46, "anchor", 43)
	got := tracker.Snapshot().Accounts[0]
	if got.Active == nil || got.Active.UsedPercent != 43 || len(got.Adjustments) != 1 {
		t.Fatalf("source handoffs produced false adjustments or lost continuity: %#v", got)
	}
}

func TestConflictingConsumersCannotMutateHistoryUntilTheyConverge(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	email := "shared@example.com"
	state, err := New([]Identity{{Username: "alpha", UID: 48}, {Username: "beta", UID: 49}}, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return now }
	tracker, err := usagehistory.Open("", 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	state.SetHistory(tracker)
	oldReset := now.Add(5 * 24 * time.Hour).Unix()
	initial := testSnapshot("alpha", now, 80)
	initial.Account.Email = &email
	initial.MainUsage.ResetsAt = &oldReset
	if err := state.Apply(48, initial); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Second)
	newReset := now.Add(7 * 24 * time.Hour).Unix()
	transition := testSnapshot("beta", now, 0)
	transition.Account.Email = &email
	transition.MainUsage.ResetsAt = &newReset
	if err := state.Apply(49, transition); err != nil {
		t.Fatal(err)
	}
	status := state.Status().Accounts[0]
	got := tracker.Snapshot().Accounts[0]
	if !status.SourceConflict || got.Active == nil || got.Active.ResetsAt != oldReset ||
		len(got.Events) != 0 || len(got.Adjustments) != 0 {
		t.Fatalf("conflicting duplicate mutated history: status=%#v history=%#v", status, got)
	}

	now = now.Add(time.Second)
	converged := testSnapshot("alpha", now, 0)
	converged.Account.Email = &email
	converged.MainUsage.ResetsAt = &newReset
	if err := state.Apply(48, converged); err != nil {
		t.Fatal(err)
	}
	got = tracker.Snapshot().Accounts[0]
	if len(got.Adjustments) != 1 || len(got.ResetPoints) != 1 ||
		got.ResetPoints[0].Kind != usagehistory.ResetPointInferredEarly {
		t.Fatalf("converged collectors did not record genuine transition: %#v", got)
	}
}

func TestFirstCanonicalAfterRestartRebaselinesLoadedHistory(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	email := "shared@example.com"
	path := filepath.Join(t.TempDir(), "account-history.json")
	before, err := usagehistory.Open(path, 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldReset := now.Add(5 * 24 * time.Hour).Unix()
	old := testSnapshot("old-consumer", now, 55)
	old.Account.Email = &email
	old.MainUsage.ResetsAt = &oldReset
	before.Observe(old)

	reloaded, err := usagehistory.Open(path, 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	state, err := New([]Identity{{Username: "new-anchor", UID: 51, ExpectedEmail: email}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	state.now = func() time.Time { return now }
	state.SetHistory(reloaded)

	// Both the lower usage and materially different future reset would be
	// adjustments under a normal same-source observation. The dashboard has
	// intentionally forgotten the prior source across restart, so it must
	// establish a new baseline without guessing.
	newReset := oldReset + int64(time.Hour/time.Second)
	current := testSnapshot("new-anchor", now, 40)
	current.Account.Email = &email
	current.MainUsage.ResetsAt = &newReset
	if err := state.Apply(51, current); err != nil {
		t.Fatal(err)
	}
	got := reloaded.Snapshot()
	if len(got.Accounts) != 1 || got.Accounts[0].Active == nil ||
		got.Accounts[0].Active.UsedPercent != 40 || got.Accounts[0].Active.ResetsAt != newReset ||
		len(got.Accounts[0].Events) != 0 || len(got.Accounts[0].Adjustments) != 0 {
		t.Fatalf("post-restart history was not safely rebaselined: %#v", got)
	}
}

func TestFirstCanonicalAfterRestartPreservesGenuineEarlyReset(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	email := "shared@example.com"
	path := filepath.Join(t.TempDir(), "account-history.json")
	before, err := usagehistory.Open(path, 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldReset := now.Add(5 * 24 * time.Hour).Unix()
	old := testSnapshot("old-consumer", now, 85)
	old.Account.Email = &email
	old.MainUsage.ResetsAt = &oldReset
	before.Observe(old)

	reloaded, err := usagehistory.Open(path, 366*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	state, err := New([]Identity{{Username: "new-anchor", UID: 50, ExpectedEmail: email}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now = time.Now().UTC().Truncate(time.Second)
	state.now = func() time.Time { return now }
	state.SetHistory(reloaded)
	newReset := now.Add(7 * 24 * time.Hour).Unix()
	current := testSnapshot("new-anchor", now, 0)
	current.Account.Email = &email
	current.MainUsage.ResetsAt = &newReset
	if err := state.Apply(50, current); err != nil {
		t.Fatal(err)
	}
	got := reloaded.Snapshot().Accounts[0]
	if len(got.Adjustments) != 1 || len(got.ResetPoints) != 1 ||
		got.ResetPoints[0].Kind != usagehistory.ResetPointInferredEarly || got.ResetPoints[0].At != now.Unix() {
		t.Fatalf("first post-restart observation erased genuine reset: %#v", got)
	}
}

func TestCanonicalReturnAfterMembershipDiscontinuityRebaselinesHistory(t *testing.T) {
	for _, test := range []struct {
		name      string
		interrupt func(time.Time) model.Snapshot
	}{
		{
			name: "signed out",
			interrupt: func(at time.Time) model.Snapshot {
				return model.Snapshot{
					SchemaVersion: model.SchemaVersion, Username: "consumer", State: model.StateSignedOut,
					Limits: []model.RateLimit{}, ObservedAt: at,
				}
			},
		},
		{
			name: "different account",
			interrupt: func(at time.Time) model.Snapshot {
				snapshot := testSnapshot("consumer", at, 20)
				email := "other@example.com"
				snapshot.Account.Email = &email
				return snapshot
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			state, err := New([]Identity{{Username: "consumer", UID: 61}}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			state.now = func() time.Time { return now }
			tracker, err := usagehistory.Open("", 366*24*time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			state.SetHistory(tracker)
			email := "original@example.com"
			resetAt := now.Add(6 * 24 * time.Hour).Unix()
			initial := testSnapshot("consumer", now, 60)
			initial.Account.Email = &email
			initial.MainUsage.ResetsAt = &resetAt
			if err := state.Apply(61, initial); err != nil {
				t.Fatal(err)
			}

			now = now.Add(time.Second)
			if err := state.Apply(61, test.interrupt(now)); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Second)
			returned := testSnapshot("consumer", now, 35)
			returned.Account.Email = &email
			returned.MainUsage.ResetsAt = &resetAt
			if err := state.Apply(61, returned); err != nil {
				t.Fatal(err)
			}

			got := tracker.Snapshot()
			var original *usagehistory.AccountHistory
			for index := range got.Accounts {
				account := &got.Accounts[index]
				if account.AccountKey == model.AccountKey(email) {
					original = account
					break
				}
			}
			if original == nil || original.Active == nil || original.Active.UsedPercent != 35 ||
				len(original.Events) != 0 || len(original.Adjustments) != 0 {
				t.Fatalf("returned account was not rebaselined: %#v", got)
			}
		})
	}
}
