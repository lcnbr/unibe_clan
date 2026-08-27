package codex

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
	AvailableCount int64 `json:"availableCount"`
}
