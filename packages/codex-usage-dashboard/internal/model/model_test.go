package model

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestAccountKeyNormalizesOnlyTrimAndCase(t *testing.T) {
	want := "f437a1788ce98310abac975b885c8a54ad7149cb82043418ea61a3468756f502"
	for _, email := range []string{
		"localunitarity+1@gmail.com",
		"  LocalUnitarity+1@GMAIL.COM\t",
	} {
		if got := AccountKey(email); got != want {
			t.Fatalf("AccountKey(%q) = %q, want %q", email, got, want)
		}
	}
	keys := map[string]bool{
		AccountKey("localunitarity@gmail.com"):    true,
		AccountKey("localunitarity+1@gmail.com"):  true,
		AccountKey("local.unitarity+1@gmail.com"): true,
	}
	if len(keys) != 3 {
		t.Fatalf("Gmail dots or plus suffixes were collapsed: %#v", keys)
	}
}

func TestSanitizeTaskNameIsSingleLineAndByteBounded(t *testing.T) {
	got := SanitizeTaskName("  investigate\nsecret\t failure\x00  ")
	if got != "investigate secret failure" {
		t.Fatalf("sanitized task name = %q", got)
	}
	long := SanitizeTaskName(strings.Repeat("é", MaxTaskNameBytes))
	if len(long) > MaxTaskNameBytes || !utf8.ValidString(long) {
		t.Fatalf("task-name cap split UTF-8: length=%d value=%q", len(long), long)
	}
	if got := SanitizeTaskName("\n\t\x00"); got != "Codex task" {
		t.Fatalf("empty sanitized name = %q", got)
	}
	spoofed := "review \u202eemanresu — admin\u2066 \ue000task"
	if got := SanitizeTaskName(spoofed); got != "review emanresu — admin task" {
		t.Fatalf("format/private-use controls survived sanitization: %q", got)
	}
}

func TestCodexVersionIsARestrictedBoundedToken(t *testing.T) {
	for _, value := range []string{"0.149.0", "0.150.0-alpha.1+build_2", "dev"} {
		if got := SanitizeCodexVersion(value); got != value {
			t.Fatalf("SanitizeCodexVersion(%q) = %q", value, got)
		}
		snapshot := Snapshot{
			SchemaVersion: SchemaVersion,
			Username:      "codex",
			CodexVersion:  value,
			State:         StateSignedOut,
			Limits:        []RateLimit{},
			ObservedAt:    time.Now().UTC(),
		}
		if err := snapshot.Validate(); err != nil {
			t.Fatalf("valid version %q: %v", value, err)
		}
	}

	for _, value := range []string{
		" 0.149.0", "0.149.0 ", "codex-cli 0.149.0", "0.149.0/path",
		"0.149.0\nforged", "0.149.0\x1b[31m", strings.Repeat("1", MaxCodexVersionBytes+1),
	} {
		if got := SanitizeCodexVersion(value); got != "" {
			t.Fatalf("unsafe version %q sanitized to %q", value, got)
		}
		snapshot := Snapshot{
			SchemaVersion: SchemaVersion,
			Username:      "codex",
			CodexVersion:  value,
			State:         StateSignedOut,
			Limits:        []RateLimit{},
			ObservedAt:    time.Now().UTC(),
		}
		if err := snapshot.Validate(); err == nil {
			t.Fatalf("unsafe version %q unexpectedly validated", value)
		}
	}
}

func TestRuntimeThreadValidationRequiresCurrentAllowlistedObservation(t *testing.T) {
	email := "person@example.com"
	snapshot := Snapshot{
		SchemaVersion: SchemaVersion,
		Username:      "codex",
		State:         StateOK,
		Account:       &Account{Type: "chatgpt", Email: &email, PlanType: "pro"},
		Limits:        []RateLimit{},
		RuntimeThreads: []RuntimeThread{{
			ThreadID: "opaque-local-id", TaskName: "Safe task", CreatedAt: 10, UpdatedAt: 20, Running: true,
		}},
		RuntimeThreadsRead: true,
		ObservedAt:         time.Now().UTC(),
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("valid runtime observation: %v", err)
	}
	snapshot.RuntimeThreadsRead = false
	if err := snapshot.Validate(); err == nil {
		t.Fatal("runtime metadata without a successful read unexpectedly validated")
	}
	snapshot.RuntimeThreadsRead = true
	snapshot.RuntimeThreads = append(snapshot.RuntimeThreads, snapshot.RuntimeThreads[0])
	if err := snapshot.Validate(); err == nil {
		t.Fatal("duplicate runtime thread unexpectedly validated")
	}
}

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
	unsafeEmail := "person\u202e@example.com"
	unsafeDisplay := Snapshot{
		SchemaVersion: SchemaVersion,
		Username:      "codex",
		State:         StateOK,
		Account:       &Account{Type: "chatgpt", Email: &unsafeEmail, PlanType: "pro"},
		Limits:        []RateLimit{},
		ObservedAt:    time.Now().UTC(),
	}
	if err := unsafeDisplay.Validate(); err == nil {
		t.Fatal("bidi control in a public display field unexpectedly validated")
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
