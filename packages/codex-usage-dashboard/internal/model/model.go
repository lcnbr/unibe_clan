package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

const (
	SchemaVersion            = 1
	MaxResetCreditsAvailable = int64(9_007_199_254_740_991)
)

type State string

const (
	StateOK          State = "ok"
	StateSignedOut   State = "signed_out"
	StateAPIKey      State = "api_key"
	StateUnavailable State = "unavailable"
)

const (
	ErrorAwaitingCollector = "awaiting_collector"
	ErrorAuthUnavailable   = "auth_unavailable"
	ErrorCodexUnavailable  = "codex_unavailable"
	ErrorProtocol          = "protocol_error"
	ErrorRateLimitRead     = "rate_limit_read_failed"
	ErrorPublish           = "publish_failed"
)

type Account struct {
	Type     string  `json:"type"`
	Email    *string `json:"email,omitempty"`
	PlanType string  `json:"planType,omitempty"`
}

type Window struct {
	UsedPercent        int    `json:"usedPercent"`
	RemainingPercent   int    `json:"remainingPercent"`
	WindowDurationMins *int64 `json:"windowDurationMins,omitempty"`
	ResetsAt           *int64 `json:"resetsAt,omitempty"`
}

type Credits struct {
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance,omitempty"`
}

type IndividualLimit struct {
	Limit            string `json:"limit"`
	Used             string `json:"used"`
	RemainingPercent int    `json:"remainingPercent"`
	ResetsAt         int64  `json:"resetsAt"`
}

type RateLimit struct {
	ID                  string           `json:"id"`
	Name                *string          `json:"name,omitempty"`
	PlanType            *string          `json:"planType,omitempty"`
	Primary             *Window          `json:"primary,omitempty"`
	Secondary           *Window          `json:"secondary,omitempty"`
	Credits             *Credits         `json:"credits,omitempty"`
	IndividualLimit     *IndividualLimit `json:"individualLimit,omitempty"`
	SpendControlReached *bool            `json:"spendControlReached,omitempty"`
	ReachedType         *string          `json:"reachedType,omitempty"`
}

type Snapshot struct {
	SchemaVersion         int         `json:"schemaVersion"`
	Username              string      `json:"username"`
	State                 State       `json:"state"`
	Account               *Account    `json:"account,omitempty"`
	MainUsage             *Window     `json:"mainUsage,omitempty"`
	ResetCreditsAvailable *int64      `json:"resetCreditsAvailable,omitempty"`
	Limits                []RateLimit `json:"limits"`
	ObservedAt            time.Time   `json:"observedAt"`
	ErrorCategory         string      `json:"errorCategory,omitempty"`
}

type AccountStatus struct {
	Snapshot
	LastSeenAt time.Time  `json:"lastSeenAt"`
	LastGoodAt *time.Time `json:"lastGoodAt,omitempty"`
	Stale      bool       `json:"stale"`
}

type StatusResponse struct {
	SchemaVersion int             `json:"schemaVersion"`
	Revision      uint64          `json:"revision"`
	GeneratedAt   time.Time       `json:"generatedAt"`
	Demo          bool            `json:"demo,omitempty"`
	Accounts      []AccountStatus `json:"accounts"`
}

var allowedErrors = map[string]bool{
	"":                     true,
	ErrorAwaitingCollector: true,
	ErrorAuthUnavailable:   true,
	ErrorCodexUnavailable:  true,
	ErrorProtocol:          true,
	ErrorRateLimitRead:     true,
	ErrorPublish:           true,
}

func (s *Snapshot) Normalize() {
	s.SchemaVersion = SchemaVersion
	normalizeWindow(s.MainUsage)
	for i := range s.Limits {
		normalizeWindow(s.Limits[i].Primary)
		normalizeWindow(s.Limits[i].Secondary)
		if l := s.Limits[i].IndividualLimit; l != nil {
			l.RemainingPercent = clampPercent(l.RemainingPercent)
		}
	}
	if s.Limits == nil {
		s.Limits = []RateLimit{}
	}
}

func normalizeWindow(w *Window) {
	if w == nil {
		return
	}
	w.UsedPercent = clampPercent(w.UsedPercent)
	w.RemainingPercent = 100 - w.UsedPercent
}

func clampPercent(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (s Snapshot) Validate() error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", s.SchemaVersion)
	}
	if err := validateText("username", s.Username, 64, false); err != nil {
		return err
	}
	switch s.State {
	case StateOK, StateSignedOut, StateAPIKey, StateUnavailable:
	default:
		return fmt.Errorf("invalid state %q", s.State)
	}
	if !allowedErrors[s.ErrorCategory] {
		return errors.New("invalid error category")
	}
	if s.ObservedAt.IsZero() {
		return errors.New("observedAt is required")
	}
	if s.ObservedAt.After(time.Now().Add(5 * time.Minute)) {
		return errors.New("observedAt is too far in the future")
	}
	switch s.State {
	case StateOK:
		if s.Account == nil || s.Account.Type != "chatgpt" {
			return errors.New("ok state requires a ChatGPT account")
		}
		if s.Account.Email == nil || strings.TrimSpace(*s.Account.Email) == "" {
			return errors.New("ok state requires an account email")
		}
		if strings.TrimSpace(s.Account.PlanType) == "" {
			return errors.New("ok state requires a plan type")
		}
		if s.ErrorCategory != "" {
			return errors.New("ok state cannot include an error category")
		}
	case StateAPIKey:
		if s.Account == nil || s.Account.Type != "apiKey" {
			return errors.New("api-key state requires an apiKey account")
		}
		if s.MainUsage != nil || s.ResetCreditsAvailable != nil || len(s.Limits) != 0 || s.ErrorCategory != "" {
			return errors.New("api-key state cannot include limits or an error category")
		}
	case StateSignedOut:
		if s.Account != nil || s.MainUsage != nil || s.ResetCreditsAvailable != nil || len(s.Limits) != 0 || s.ErrorCategory != "" {
			return errors.New("signed-out state cannot include account data, limits, or errors")
		}
	case StateUnavailable:
		if s.Account != nil || s.MainUsage != nil || s.ResetCreditsAvailable != nil || len(s.Limits) != 0 {
			return errors.New("unavailable state cannot submit account data or limits")
		}
		if s.ErrorCategory == "" {
			return errors.New("unavailable state requires a safe error category")
		}
	}
	if s.Account != nil {
		if err := validateText("account type", s.Account.Type, 32, false); err != nil {
			return err
		}
		if err := validateText("plan type", s.Account.PlanType, 64, true); err != nil {
			return err
		}
		if s.Account.Email != nil {
			if err := validateText("email", *s.Account.Email, 320, true); err != nil {
				return err
			}
		}
	}
	if len(s.Limits) > 32 {
		return errors.New("too many rate-limit buckets")
	}
	if s.ResetCreditsAvailable != nil &&
		(*s.ResetCreditsAvailable < 0 || *s.ResetCreditsAvailable > MaxResetCreditsAvailable) {
		return errors.New("available reset-credit count outside allowed range")
	}
	if s.MainUsage != nil {
		if err := validateWindow(s.MainUsage); err != nil {
			return fmt.Errorf("main usage: %w", err)
		}
		if s.MainUsage.WindowDurationMins == nil || *s.MainUsage.WindowDurationMins != 10_080 {
			return errors.New("main usage must be the canonical weekly window")
		}
		if s.MainUsage.ResetsAt == nil || *s.MainUsage.ResetsAt <= s.ObservedAt.Unix() {
			return errors.New("main usage requires a future reset timestamp")
		}
		if *s.MainUsage.ResetsAt-10_080*60 <= 0 {
			return errors.New("main usage has an invalid weekly window start")
		}
	}
	seenLimitIDs := make(map[string]bool, len(s.Limits))
	for i := range s.Limits {
		if err := s.Limits[i].validate(); err != nil {
			return fmt.Errorf("limit %d: %w", i, err)
		}
		if seenLimitIDs[s.Limits[i].ID] {
			return fmt.Errorf("limit %d: duplicate id", i)
		}
		seenLimitIDs[s.Limits[i].ID] = true
	}
	return nil
}

func (l RateLimit) validate() error {
	if err := validateText("id", l.ID, 128, false); err != nil {
		return err
	}
	for name, value := range map[string]*string{
		"name": l.Name, "plan type": l.PlanType, "reached type": l.ReachedType,
	} {
		if value != nil {
			if err := validateText(name, *value, 128, true); err != nil {
				return err
			}
		}
	}
	for _, w := range []*Window{l.Primary, l.Secondary} {
		if w == nil {
			continue
		}
		if err := validateWindow(w); err != nil {
			return err
		}
	}
	if l.Credits != nil && l.Credits.Balance != nil {
		if err := validateText("credit balance", *l.Credits.Balance, 64, true); err != nil {
			return err
		}
	}
	if l.IndividualLimit != nil {
		if err := validateText("individual limit", l.IndividualLimit.Limit, 64, false); err != nil {
			return err
		}
		if err := validateText("individual usage", l.IndividualLimit.Used, 64, false); err != nil {
			return err
		}
		if l.IndividualLimit.RemainingPercent < 0 || l.IndividualLimit.RemainingPercent > 100 {
			return errors.New("individual remaining percentage outside 0..100")
		}
		if l.IndividualLimit.ResetsAt <= 0 || l.IndividualLimit.ResetsAt > 32_503_680_000 {
			return errors.New("invalid individual reset timestamp")
		}
	}
	return nil
}

func validateWindow(w *Window) error {
	if w.UsedPercent < 0 || w.UsedPercent > 100 || w.RemainingPercent < 0 || w.RemainingPercent > 100 {
		return errors.New("percentage outside 0..100")
	}
	if w.UsedPercent+w.RemainingPercent != 100 {
		return errors.New("used and remaining percentages do not total 100")
	}
	if w.WindowDurationMins != nil && (*w.WindowDurationMins <= 0 || *w.WindowDurationMins > 5_256_000) {
		return errors.New("invalid window duration")
	}
	if w.ResetsAt != nil && (*w.ResetsAt <= 0 || *w.ResetsAt > 32_503_680_000) {
		return errors.New("invalid reset timestamp")
	}
	return nil
}

func validateText(name, value string, max int, allowEmpty bool) error {
	if !allowEmpty && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > max {
		return fmt.Errorf("%s is too long", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}
