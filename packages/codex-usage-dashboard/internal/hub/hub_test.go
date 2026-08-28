package hub

import (
	"testing"
	"time"

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
	if got := state.Status().Accounts[0].ErrorCategory; got != model.ErrorAwaitingCollector {
		t.Fatalf("stored state changed after rejection: %q", got)
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
	if status.Stale {
		t.Fatal("last-good data became stale too early")
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
	got := state.Status().Accounts[0]
	if got.Account != nil || got.MainUsage != nil || got.ResetCreditsAvailable != nil || len(got.Limits) != 0 || got.Stale {
		t.Fatalf("signed-out snapshot retained account data: %#v", got)
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
