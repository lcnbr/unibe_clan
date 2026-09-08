package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	SchemaVersion            = 2
	MaxResetCreditsAvailable = int64(9_007_199_254_740_991)
	MaxLifetimeTokens        = int64(9_007_199_254_740_991)
	MaxRecentThreads         = 64
	MaxRuntimeThreads        = 64
	MaxTaskNameBytes         = 96
	MaxCodexVersionBytes     = 64
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

// AccountKey returns the stable, opaque identity used outside collector
// memory. Deliberately do not apply provider-specific rewriting: Gmail dots
// and plus suffixes identify distinct dashboard accounts.
func AccountKey(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	digest := sha256.Sum256(append([]byte("chatgpt\x00"), []byte(normalized)...))
	return hex.EncodeToString(digest[:])
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
	SchemaVersion          int             `json:"schemaVersion"`
	Username               string          `json:"username"`
	CodexVersion           string          `json:"codexVersion,omitempty"`
	CodexVersionObservedAt *time.Time      `json:"codexVersionObservedAt,omitempty"`
	State                  State           `json:"state"`
	Account                *Account        `json:"account,omitempty"`
	MainUsage              *Window         `json:"mainUsage,omitempty"`
	ResetCreditsAvailable  *int64          `json:"resetCreditsAvailable,omitempty"`
	LifetimeTokens         *int64          `json:"lifetimeTokens,omitempty"`
	LifetimeTokensRead     bool            `json:"lifetimeTokensRead,omitempty"`
	Limits                 []RateLimit     `json:"limits"`
	RecentThreads          []RecentThread  `json:"recentThreads,omitempty"`
	RecentThreadsRead      bool            `json:"recentThreadsRead,omitempty"`
	RuntimeThreads         []RuntimeThread `json:"runtimeThreads,omitempty"`
	RuntimeThreadsRead     bool            `json:"runtimeThreadsRead,omitempty"`
	ObservedAt             time.Time       `json:"observedAt"`
	ErrorCategory          string          `json:"errorCategory,omitempty"`
}

// RecentThread is collector-private metadata used to turn an opaque hook
// session identifier into a safe display name. ThreadID contains App Server's
// sessionId (the field name is retained for wire compatibility), never a
// thread-tree child ID, and is never copied into StatusResponse, logs, or
// history.
type RecentThread struct {
	ThreadID  string `json:"threadId"`
	TaskName  string `json:"taskName"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// RuntimeThread is an ephemeral observation from the already-running user's
// App Server control socket. ThreadID contains App Server's sessionId and is
// used only to merge it with hook state in memory; it must never be copied
// into public status, logs, or history.
// Running distinguishes a turn currently executing from a loaded, idle chat.
type RuntimeThread struct {
	ThreadID  string `json:"threadId"`
	TaskName  string `json:"taskName"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	Running   bool   `json:"running"`
}

type UserRole string

const (
	UserRoleAnchor   UserRole = "anchor"
	UserRoleConsumer UserRole = "consumer"
)

type AnchorHealth string

const (
	AnchorHealthOK           AnchorHealth = "ok"
	AnchorHealthStale        AnchorHealth = "stale"
	AnchorHealthSignedOut    AnchorHealth = "signed_out"
	AnchorHealthWrongAccount AnchorHealth = "wrong_account"
)

type UserStatus struct {
	Username               string     `json:"username"`
	CodexVersion           string     `json:"codexVersion,omitempty"`
	CodexVersionObservedAt *time.Time `json:"codexVersionObservedAt,omitempty"`
	ActiveChatsKnown       bool       `json:"activeChatsKnown"`
	State                  State      `json:"state"`
	Role                   UserRole   `json:"role"`
	LastSeenAt             time.Time  `json:"lastSeenAt"`
	LastGoodAt             *time.Time `json:"lastGoodAt,omitempty"`
	Stale                  bool       `json:"stale"`
}

type ActiveChat struct {
	TaskName  string    `json:"taskName"`
	Username  string    `json:"username"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Running   bool      `json:"running"`
}

// AccountStatus contains one canonical, never-summed account observation and
// the Linux users currently associated with it. AccountKey is safe to retain;
// the remaining identity and activity fields are public-response-only data.
type AccountStatus struct {
	AccountKey            string       `json:"accountKey"`
	State                 State        `json:"state"`
	Account               *Account     `json:"account,omitempty"`
	MainUsage             *Window      `json:"mainUsage,omitempty"`
	ResetCreditsAvailable *int64       `json:"resetCreditsAvailable,omitempty"`
	Limits                []RateLimit  `json:"limits"`
	ObservedAt            time.Time    `json:"observedAt"`
	LastSeenAt            time.Time    `json:"lastSeenAt"`
	LastGoodAt            *time.Time   `json:"lastGoodAt,omitempty"`
	Stale                 bool         `json:"stale"`
	Users                 []UserStatus `json:"users"`
	ActiveChats           []ActiveChat `json:"activeChats"`
	LifetimeTokens        *int64       `json:"lifetimeTokens"`
	AnchorHealth          AnchorHealth `json:"anchorHealth,omitempty"`
	SourceConflict        bool         `json:"sourceConflict,omitempty"`
}

type StatusResponse struct {
	SchemaVersion   int             `json:"schemaVersion"`
	Revision        uint64          `json:"revision"`
	GeneratedAt     time.Time       `json:"generatedAt"`
	Demo            bool            `json:"demo,omitempty"`
	Accounts        []AccountStatus `json:"accounts"`
	UnassignedUsers []UserStatus    `json:"unassignedUsers"`
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

var codexVersionPattern = regexp.MustCompile(
	`^(?:dev|[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}(?:-[0-9A-Za-z][0-9A-Za-z._-]{0,31})?(?:\+[0-9A-Za-z][0-9A-Za-z._-]{0,31})?)$`,
)

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
	if s.CodexVersion != SanitizeCodexVersion(s.CodexVersion) {
		return errors.New("invalid Codex version")
	}
	if s.CodexVersion == "" && s.CodexVersionObservedAt != nil {
		return errors.New("Codex version timestamp requires a version")
	}
	if s.CodexVersion != "" {
		if s.CodexVersionObservedAt == nil || s.CodexVersionObservedAt.IsZero() {
			return errors.New("Codex version requires an observation timestamp")
		}
		if !s.CodexVersionObservedAt.Equal(s.ObservedAt) {
			return errors.New("Codex version timestamp must match the collector observation")
		}
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
		if s.MainUsage != nil || s.ResetCreditsAvailable != nil || s.LifetimeTokens != nil || s.LifetimeTokensRead || len(s.Limits) != 0 || len(s.RecentThreads) != 0 || s.RecentThreadsRead || len(s.RuntimeThreads) != 0 || s.RuntimeThreadsRead || s.ErrorCategory != "" {
			return errors.New("api-key state cannot include limits or an error category")
		}
	case StateSignedOut:
		if s.Account != nil || s.MainUsage != nil || s.ResetCreditsAvailable != nil || s.LifetimeTokens != nil || s.LifetimeTokensRead || len(s.Limits) != 0 || len(s.RecentThreads) != 0 || s.RecentThreadsRead || len(s.RuntimeThreads) != 0 || s.RuntimeThreadsRead || s.ErrorCategory != "" {
			return errors.New("signed-out state cannot include account data, limits, or errors")
		}
	case StateUnavailable:
		if s.Account != nil || s.MainUsage != nil || s.ResetCreditsAvailable != nil || s.LifetimeTokens != nil || s.LifetimeTokensRead || len(s.Limits) != 0 || len(s.RecentThreads) != 0 || s.RecentThreadsRead || len(s.RuntimeThreads) != 0 || s.RuntimeThreadsRead {
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
	if s.LifetimeTokens != nil && (*s.LifetimeTokens < 0 || *s.LifetimeTokens > MaxLifetimeTokens) {
		return errors.New("lifetime token count outside allowed range")
	}
	if s.LifetimeTokens != nil && !s.LifetimeTokensRead {
		return errors.New("lifetime token count requires a successful usage read")
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
	if len(s.RecentThreads) > MaxRecentThreads {
		return errors.New("too many recent threads")
	}
	if len(s.RecentThreads) != 0 && !s.RecentThreadsRead {
		return errors.New("recent threads require a successful thread-list read")
	}
	seenThreadIDs := make(map[string]bool, len(s.RecentThreads))
	for index := range s.RecentThreads {
		thread := s.RecentThreads[index]
		if err := validateText("thread id", thread.ThreadID, 128, false); err != nil {
			return fmt.Errorf("recent thread %d: %w", index, err)
		}
		if err := validateText("task name", thread.TaskName, MaxTaskNameBytes, false); err != nil {
			return fmt.Errorf("recent thread %d: %w", index, err)
		}
		if thread.CreatedAt < 0 || thread.CreatedAt > 32_503_680_000 ||
			thread.UpdatedAt < 0 || thread.UpdatedAt > 32_503_680_000 ||
			(thread.CreatedAt != 0 && thread.UpdatedAt != 0 && thread.UpdatedAt < thread.CreatedAt) {
			return fmt.Errorf("recent thread %d: invalid timestamps", index)
		}
		if seenThreadIDs[thread.ThreadID] {
			return fmt.Errorf("recent thread %d: duplicate id", index)
		}
		seenThreadIDs[thread.ThreadID] = true
	}
	if len(s.RuntimeThreads) > MaxRuntimeThreads {
		return errors.New("too many runtime threads")
	}
	if len(s.RuntimeThreads) != 0 && !s.RuntimeThreadsRead {
		return errors.New("runtime threads require a successful control-socket read")
	}
	seenThreadIDs = make(map[string]bool, len(s.RuntimeThreads))
	for index := range s.RuntimeThreads {
		thread := s.RuntimeThreads[index]
		if err := validateText("thread id", thread.ThreadID, 128, false); err != nil {
			return fmt.Errorf("runtime thread %d: %w", index, err)
		}
		if err := validateText("task name", thread.TaskName, MaxTaskNameBytes, false); err != nil {
			return fmt.Errorf("runtime thread %d: %w", index, err)
		}
		if thread.CreatedAt < 0 || thread.CreatedAt > 32_503_680_000 ||
			thread.UpdatedAt < 0 || thread.UpdatedAt > 32_503_680_000 ||
			(thread.CreatedAt != 0 && thread.UpdatedAt != 0 && thread.UpdatedAt < thread.CreatedAt) {
			return fmt.Errorf("runtime thread %d: invalid timestamps", index)
		}
		if seenThreadIDs[thread.ThreadID] {
			return fmt.Errorf("runtime thread %d: duplicate id", index)
		}
		seenThreadIDs[thread.ThreadID] = true
	}
	return nil
}

// SanitizeCodexVersion accepts only a bounded single-token CLI version.
// Product names, paths, terminal escapes, and arbitrary text are deliberately
// excluded from collector snapshots and activity observations.
func SanitizeCodexVersion(value string) string {
	if value == "" || value != strings.TrimSpace(value) || len(value) > MaxCodexVersionBytes ||
		!codexVersionPattern.MatchString(value) {
		return ""
	}
	return value
}

// SanitizeTaskName produces a bounded single-line name. It intentionally
// accepts only the explicit thread name field, never prompts or previews.
func SanitizeTaskName(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "Codex task"
	}
	var builder strings.Builder
	for _, char := range value {
		// Format controls (including bidi isolates/overrides), surrogates,
		// and private-use runes can visually spoof the appended username even
		// though the browser inserts this text safely with textContent.
		if unsafeDisplayRune(char) {
			continue
		}
		if builder.Len()+len(string(char)) > MaxTaskNameBytes {
			break
		}
		builder.WriteRune(char)
	}
	name := strings.TrimSpace(builder.String())
	if name == "" {
		return "Codex task"
	}
	return name
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
		if unsafeDisplayRune(r) {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func unsafeDisplayRune(char rune) bool {
	return unicode.IsControl(char) || unicode.Is(unicode.Cf, char) ||
		unicode.Is(unicode.Cs, char) || unicode.Is(unicode.Co, char)
}
