package collector

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"codex-usage-dashboard/internal/codex"
	"codex-usage-dashboard/internal/model"
)

func stringPointer(value string) *string { return &value }
func int64Pointer(value int64) *int64    { return &value }

func TestSnapshotForAccountStrictStates(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name     string
		account  *codex.Account
		state    model.State
		hasModel bool
		category string
	}{
		{name: "signed out", state: model.StateSignedOut},
		{name: "api key", account: &codex.Account{Type: "apiKey"}, state: model.StateAPIKey, hasModel: true},
		{name: "chatgpt", account: &codex.Account{Type: "chatgpt", Email: stringPointer("person@example.com"), PlanType: stringPointer("pro")}, state: model.StateOK, hasModel: true},
		{name: "missing email", account: &codex.Account{Type: "chatgpt", PlanType: stringPointer("pro")}, state: model.StateUnavailable, category: model.ErrorAuthUnavailable},
		{name: "missing plan", account: &codex.Account{Type: "chatgpt", Email: stringPointer("person@example.com")}, state: model.StateUnavailable, category: model.ErrorProtocol},
		{name: "unknown provider", account: &codex.Account{Type: "future", Email: stringPointer("private@example.com")}, state: model.StateUnavailable, category: model.ErrorAuthUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := snapshotForAccount("codex-2", now, codex.AccountResponse{Account: test.account})
			if snapshot.State != test.state || (snapshot.Account != nil) != test.hasModel || snapshot.ErrorCategory != test.category {
				t.Fatalf("unexpected snapshot: %#v", snapshot)
			}
			snapshot.Normalize()
			if err := snapshot.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestSanitizeLimitsBoundsAndDeduplicates(t *testing.T) {
	tooLongDuration := int64(5_256_001)
	tooLateReset := int64(32_503_680_001)
	validDuration := int64(300)
	validReset := int64(1_800_000_000)
	duplicate := "same"
	response := codex.RateLimitsResponse{RateLimitsByLimitID: map[string]codex.RateLimitSnapshot{
		"a": {
			LimitID: &duplicate,
			Primary: &codex.RateLimitWindow{UsedPercent: -20, WindowDurationMins: &tooLongDuration, ResetsAt: &tooLateReset},
		},
		"b": {
			LimitID:         &duplicate,
			Primary:         &codex.RateLimitWindow{UsedPercent: 120, WindowDurationMins: &validDuration, ResetsAt: &validReset},
			IndividualLimit: &codex.SpendControlLimitSnapshot{Limit: "10", Used: "2", RemainingPercent: 101, ResetsAt: tooLateReset},
		},
	}}
	limits := sanitizeLimits(response)
	if len(limits) != 2 {
		t.Fatalf("got %d limits", len(limits))
	}
	if limits[0].ID == limits[1].ID {
		t.Fatalf("duplicate final IDs: %q", limits[0].ID)
	}
	if limits[0].Primary.UsedPercent != 0 || limits[0].Primary.WindowDurationMins != nil || limits[0].Primary.ResetsAt != nil {
		t.Fatalf("invalid values were not removed: %#v", limits[0].Primary)
	}
	if limits[1].Primary.UsedPercent != 100 || limits[1].Primary.RemainingPercent != 0 || limits[1].Primary.WindowDurationMins == nil || limits[1].Primary.ResetsAt == nil {
		t.Fatalf("valid values were not retained: %#v", limits[1].Primary)
	}
	if limits[1].IndividualLimit != nil {
		t.Fatalf("invalid individual limit retained: %#v", limits[1].IndividualLimit)
	}
	snapshot := model.Snapshot{
		Username: "codex-2", State: model.StateOK,
		Account: &model.Account{Type: "chatgpt", Email: stringPointer("person@example.com"), PlanType: "pro"},
		Limits:  limits, ObservedAt: time.Now().UTC(),
	}
	snapshot.Normalize()
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("sanitized snapshot failed validation: %v", err)
	}
}

func TestSanitizeLimitsCapsBucketsAndText(t *testing.T) {
	buckets := make(map[string]codex.RateLimitSnapshot)
	for i := 0; i < 40; i++ {
		id := strings.Repeat("x", 200)
		buckets[fmt.Sprintf("bucket-%02d", i)] = codex.RateLimitSnapshot{LimitID: &id}
	}
	limits := sanitizeLimits(codex.RateLimitsResponse{RateLimitsByLimitID: buckets})
	if len(limits) != 32 {
		t.Fatalf("got %d buckets, want 32", len(limits))
	}
	seen := make(map[string]bool)
	for _, limit := range limits {
		if limit.ID == "" || len(limit.ID) > 128 || seen[limit.ID] {
			t.Fatalf("invalid sanitized id %q", limit.ID)
		}
		seen[limit.ID] = true
	}
}

func TestSanitizeMainUsageUsesOnlyCanonicalTopLevelWeeklyWindow(t *testing.T) {
	shortDuration := int64(300)
	weeklyDuration := int64(10_080)
	reset := int64(1_800_000_000)
	sparkName := "GPT-5.3-Codex-Spark"
	response := codex.RateLimitsResponse{
		RateLimits: codex.RateLimitSnapshot{
			Primary: &codex.RateLimitWindow{
				UsedPercent: 88, WindowDurationMins: &shortDuration, ResetsAt: &reset,
			},
			Secondary: &codex.RateLimitWindow{
				UsedPercent: 37, WindowDurationMins: &weeklyDuration, ResetsAt: &reset,
			},
		},
		RateLimitsByLimitID: map[string]codex.RateLimitSnapshot{
			"codex_bengalfox": {
				LimitName: &sparkName,
				Secondary: &codex.RateLimitWindow{
					UsedPercent: 91, WindowDurationMins: &weeklyDuration, ResetsAt: &reset,
				},
			},
			"gpt-reserve": {
				Primary: &codex.RateLimitWindow{
					UsedPercent: 55, WindowDurationMins: &weeklyDuration, ResetsAt: &reset,
				},
			},
		},
	}

	got := sanitizeMainUsage(response)
	if got == nil || got.UsedPercent != 37 || got.WindowDurationMins == nil ||
		*got.WindowDurationMins != weeklyDuration {
		t.Fatalf("canonical main usage = %#v", got)
	}

	response.RateLimits.Secondary = nil
	if got := sanitizeMainUsage(response); got != nil {
		t.Fatalf("map bucket was used as canonical fallback: %#v", got)
	}
}

func TestSanitizeResetCreditsPreservesZeroAndRejectsInvalidCounts(t *testing.T) {
	zero := int64(0)
	positive := int64(3)
	negative := int64(-1)
	tooLarge := model.MaxResetCreditsAvailable + 1
	tests := []struct {
		name     string
		response codex.RateLimitsResponse
		want     *int64
	}{
		{name: "object unavailable"},
		{name: "count unavailable", response: codex.RateLimitsResponse{ResetCredits: &codex.ResetCreditsSummary{}}},
		{name: "authoritative zero", response: codex.RateLimitsResponse{ResetCredits: &codex.ResetCreditsSummary{AvailableCount: &zero}}, want: &zero},
		{name: "positive", response: codex.RateLimitsResponse{ResetCredits: &codex.ResetCreditsSummary{AvailableCount: &positive}}, want: &positive},
		{name: "negative", response: codex.RateLimitsResponse{ResetCredits: &codex.ResetCreditsSummary{AvailableCount: &negative}}},
		{name: "not exactly representable in JavaScript", response: codex.RateLimitsResponse{ResetCredits: &codex.ResetCreditsSummary{AvailableCount: &tooLarge}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sanitizeResetCredits(test.response)
			if test.want == nil && got != nil {
				t.Fatalf("got %d, want unavailable", *got)
			}
			if test.want != nil && (got == nil || *got != *test.want) {
				t.Fatalf("got %v, want %d", got, *test.want)
			}
		})
	}
}
