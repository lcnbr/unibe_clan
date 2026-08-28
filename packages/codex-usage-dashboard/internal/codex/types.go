package codex

import (
	"bytes"
	"encoding/json"
)

// AccountResponse is the allowlisted portion of account/read.
type AccountResponse struct {
	Account            *Account `json:"account"`
	RequiresOpenAIAuth bool     `json:"requiresOpenaiAuth"`
}

// Account contains only non-secret account display fields. App Server can
// return provider-specific fields; the dashboard deliberately ignores them.
type Account struct {
	Type     string  `json:"type"`
	Email    *string `json:"email,omitempty"`
	PlanType *string `json:"planType,omitempty"`
}

// RateLimitsResponse is the allowlisted portion of account/rateLimits/read.
type RateLimitsResponse struct {
	RateLimits          RateLimitSnapshot            `json:"rateLimits"`
	RateLimitsByLimitID map[string]RateLimitSnapshot `json:"rateLimitsByLimitId"`
	ResetCredits        *ResetCreditsSummary         `json:"rateLimitResetCredits"`
}

type RateLimitSnapshot struct {
	LimitID              *string                    `json:"limitId"`
	LimitName            *string                    `json:"limitName"`
	PlanType             *string                    `json:"planType"`
	Primary              *RateLimitWindow           `json:"primary"`
	Secondary            *RateLimitWindow           `json:"secondary"`
	Credits              *CreditsSnapshot           `json:"credits"`
	IndividualLimit      *SpendControlLimitSnapshot `json:"individualLimit"`
	SpendControlReached  *bool                      `json:"spendControlReached"`
	RateLimitReachedType *string                    `json:"rateLimitReachedType"`
}

type RateLimitWindow struct {
	UsedPercent        int    `json:"usedPercent"`
	WindowDurationMins *int64 `json:"windowDurationMins"`
	ResetsAt           *int64 `json:"resetsAt"`
}

type CreditsSnapshot struct {
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance"`
}

type SpendControlLimitSnapshot struct {
	Limit            string `json:"limit"`
	Used             string `json:"used"`
	RemainingPercent int    `json:"remainingPercent"`
	ResetsAt         int64  `json:"resetsAt"`
}

// ResetCreditsSummary intentionally omits the detail rows. Those rows contain
// opaque identifiers that must not be forwarded or logged by the collector.
type ResetCreditsSummary struct {
	AvailableCount *int64 `json:"availableCount"`
}

// AccountUsageResponse is the only account/usage/read field retained by the
// collector. Daily buckets and other account analytics are intentionally
// discarded.
type AccountUsageResponse struct {
	LifetimeTokens *int64
}

// ThreadListResponse is the allowlisted portion of thread/list. In
// particular it cannot retain preview, cwd, path, Git, provider, or turn data.
type ThreadListResponse struct {
	Threads []Thread
}

// LoadedThreadListResponse is the allowlisted portion of
// thread/loaded/list. The IDs are used only for immediate, metadata-only
// thread/read calls and are never published or logged.
type LoadedThreadListResponse struct {
	ThreadIDs []string
}

type Thread struct {
	ID             string        `json:"id"`
	SessionID      string        `json:"sessionId"`
	Name           *string       `json:"name"`
	ParentThreadID *string       `json:"parentThreadId"`
	Source         SessionSource `json:"source"`
	Status         ThreadStatus  `json:"status"`
	CreatedAt      int64         `json:"createdAt"`
	UpdatedAt      int64         `json:"updatedAt"`
}

// SessionSource retains only the five explicitly supported non-subagent
// source kinds. Object-valued custom and subagent sources, and unknown future
// strings, decode to the zero value and are excluded by the collector. Their
// nested data is never retained.
type SessionSource string

const (
	SessionSourceCLI       SessionSource = "cli"
	SessionSourceVSCode    SessionSource = "vscode"
	SessionSourceExec      SessionSource = "exec"
	SessionSourceAppServer SessionSource = "appServer"
	SessionSourceUnknown   SessionSource = "unknown"
)

func (source *SessionSource) UnmarshalJSON(payload []byte) error {
	*source = ""
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return nil
	}
	if payload[0] != '"' {
		// SessionSource also permits object-valued custom and subagent
		// variants. Decode only their outer shape and deliberately retain
		// nothing from them.
		var object struct{}
		if err := json.Unmarshal(payload, &object); err != nil {
			return err
		}
		return nil
	}
	var value string
	if err := json.Unmarshal(payload, &value); err != nil {
		return err
	}
	switch candidate := SessionSource(value); candidate {
	case SessionSourceCLI, SessionSourceVSCode, SessionSourceExec,
		SessionSourceAppServer, SessionSourceUnknown:
		*source = candidate
	}
	return nil
}

func (source SessionSource) MarshalJSON() ([]byte, error) {
	if !source.Allowed() {
		return []byte("null"), nil
	}
	return json.Marshal(string(source))
}

func (source SessionSource) Allowed() bool {
	switch source {
	case SessionSourceCLI, SessionSourceVSCode, SessionSourceExec,
		SessionSourceAppServer, SessionSourceUnknown:
		return true
	default:
		return false
	}
}

// ThreadStatus retains only the documented runtime-state discriminator. In
// particular active flags and any diagnostic/system-error text are discarded.
type ThreadStatus struct {
	Type string `json:"type"`
}
