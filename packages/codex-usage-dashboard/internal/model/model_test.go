package model

import (
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
