package model

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeClampsAndComputesRemaining(t *testing.T) {
	email := "person@example.com"
	s := Snapshot{
		Username:   "codex",
		State:      StateOK,
		Account:    &Account{Type: "chatgpt", Email: &email, PlanType: "pro"},
		ObservedAt: time.Now(),
		Limits: []RateLimit{{
			ID:        "codex",
			Primary:   &Window{UsedPercent: 31},
			Secondary: &Window{UsedPercent: 120},
		}},
	}
	s.Normalize()
	if got := s.Limits[0].Primary.RemainingPercent; got != 69 {
		t.Fatalf("remaining = %d, want 69", got)
	}
	if got := s.Limits[0].Secondary.UsedPercent; got != 100 {
		t.Fatalf("clamped used = %d, want 100", got)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestResetCreditCountValidationAndJSONSemantics(t *testing.T) {
	email := "person@example.com"
	zero := int64(0)
	valid := Snapshot{
		SchemaVersion: SchemaVersion, Username: "codex", State: StateOK,
		Account:               &Account{Type: "chatgpt", Email: &email, PlanType: "pro"},
		ResetCreditsAvailable: &zero, Limits: []RateLimit{}, ObservedAt: time.Now().UTC(),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("authoritative zero did not validate: %v", err)
	}
	payload, err := json.Marshal(valid)
	if err != nil || !bytes.Contains(payload, []byte(`"resetCreditsAvailable":0`)) {
		t.Fatalf("authoritative zero was not serialized: %s (error %v)", payload, err)
	}
	valid.ResetCreditsAvailable = nil
	payload, err = json.Marshal(valid)
	if err != nil || bytes.Contains(payload, []byte("resetCreditsAvailable")) {
		t.Fatalf("unavailable count was not omitted: %s (error %v)", payload, err)
	}

	invalidCounts := []int64{-1, MaxResetCreditsAvailable + 1}
	for _, count := range invalidCounts {
		invalid := valid
		invalid.ResetCreditsAvailable = &count
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid count %d unexpectedly validated", count)
		}
	}

	for _, state := range []State{StateSignedOut, StateAPIKey, StateUnavailable} {
		invalid := Snapshot{
			SchemaVersion: SchemaVersion, Username: "codex", State: state,
			ResetCreditsAvailable: &zero, Limits: []RateLimit{}, ObservedAt: time.Now().UTC(),
		}
		if state == StateAPIKey {
			invalid.Account = &Account{Type: "apiKey"}
		}
		if state == StateUnavailable {
			invalid.ErrorCategory = ErrorCodexUnavailable
		}
		if err := invalid.Validate(); err == nil {
			t.Fatalf("count unexpectedly accepted for state %s", state)
		}
	}
}

func TestValidateStateSemantics(t *testing.T) {
	now := time.Now().UTC()
	email := "person@example.com"
	tests := []Snapshot{
		{SchemaVersion: SchemaVersion, Username: "codex", State: StateOK, ObservedAt: now, Limits: []RateLimit{}},
		{SchemaVersion: SchemaVersion, Username: "codex", State: StateOK, ObservedAt: now, Account: &Account{Type: "chatgpt", Email: &email}, Limits: []RateLimit{}},
		{SchemaVersion: SchemaVersion, Username: "codex", State: StateSignedOut, ObservedAt: now, Account: &Account{Type: "chatgpt", Email: &email}, Limits: []RateLimit{}},
		{SchemaVersion: SchemaVersion, Username: "codex", State: StateAPIKey, ObservedAt: now, Account: &Account{Type: "chatgpt", Email: &email}, Limits: []RateLimit{}},
		{SchemaVersion: SchemaVersion, Username: "codex", State: StateUnavailable, ObservedAt: now, Limits: []RateLimit{}},
	}
	for i, snapshot := range tests {
		if err := snapshot.Validate(); err == nil {
			t.Fatalf("case %d unexpectedly validated", i)
		}
	}
}

func TestValidateRejectsUnsafeOrUnknownValues(t *testing.T) {
	tests := []Snapshot{
		{SchemaVersion: 99, Username: "codex", State: StateOK, ObservedAt: time.Now()},
		{SchemaVersion: 1, Username: "codex\nforged", State: StateOK, ObservedAt: time.Now()},
		{SchemaVersion: 1, Username: "codex", State: "mystery", ObservedAt: time.Now()},
		{SchemaVersion: 1, Username: "codex", State: StateUnavailable, ErrorCategory: "raw upstream error", ObservedAt: time.Now()},
	}
	for i, tc := range tests {
		if err := tc.Validate(); err == nil {
			t.Fatalf("case %d unexpectedly valid", i)
		}
	}
}

func TestValidateRequiresCanonicalWeeklyMainUsage(t *testing.T) {
	email := "person@example.com"
	duration := int64(300)
	reset := time.Now().Add(time.Hour).Unix()
	snapshot := Snapshot{
		SchemaVersion: SchemaVersion,
		Username:      "codex",
		State:         StateOK,
		Account:       &Account{Type: "chatgpt", Email: &email, PlanType: "pro"},
		MainUsage: &Window{
			UsedPercent: 12, RemainingPercent: 88, WindowDurationMins: &duration, ResetsAt: &reset,
		},
		Limits:     []RateLimit{},
		ObservedAt: time.Now().UTC(),
	}
	if err := snapshot.Validate(); err == nil {
		t.Fatal("non-weekly main usage unexpectedly validated")
	}
}
