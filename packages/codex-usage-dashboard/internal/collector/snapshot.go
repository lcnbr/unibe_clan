package collector

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"codex-usage-dashboard/internal/codex"
	"codex-usage-dashboard/internal/model"
)

func snapshotForAccount(username string, observedAt time.Time, response codex.AccountResponse) model.Snapshot {
	snapshot := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      username,
		State:         model.StateSignedOut,
		Limits:        []model.RateLimit{},
		ObservedAt:    observedAt,
	}
	if response.Account == nil {
		return snapshot
	}

	switch response.Account.Type {
	case "chatgpt":
		email := safeOptional(response.Account.Email, 320, false)
		if email == nil {
			snapshot.State = model.StateUnavailable
			snapshot.ErrorCategory = model.ErrorAuthUnavailable
			return snapshot
		}
		plan := safeOptional(response.Account.PlanType, 64, false)
		if plan == nil {
			snapshot.State = model.StateUnavailable
			snapshot.ErrorCategory = model.ErrorProtocol
			return snapshot
		}
		account := &model.Account{Type: "chatgpt", Email: email, PlanType: *plan}
		snapshot.Account = account
		snapshot.State = model.StateOK
	case "apiKey":
		snapshot.Account = &model.Account{Type: "apiKey"}
		snapshot.State = model.StateAPIKey
	default:
		snapshot.State = model.StateUnavailable
		snapshot.ErrorCategory = model.ErrorAuthUnavailable
	}
	return snapshot
}

func sanitizeLimits(response codex.RateLimitsResponse) []model.RateLimit {
	if len(response.RateLimitsByLimitID) == 0 {
		if rateLimitIsEmpty(response.RateLimits) {
			return []model.RateLimit{}
		}
		return []model.RateLimit{sanitizeLimit(response.RateLimits, "codex", 0)}
	}

	keys := make([]string, 0, len(response.RateLimitsByLimitID))
	for key := range response.RateLimitsByLimitID {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 32 {
		keys = keys[:32]
	}
	limits := make([]model.RateLimit, 0, len(keys))
	seenIDs := make(map[string]bool, len(keys))
	for index, key := range keys {
		limit := sanitizeLimit(response.RateLimitsByLimitID[key], key, index)
		limit.ID = uniqueLimitID(limit.ID, index, seenIDs)
		limits = append(limits, limit)
	}
	return limits
}

// sanitizeMainUsage selects only the canonical top-level weekly window. The
// by-limit map contains model-specific buckets such as Spark and must never be
// used for the compact account view or reset history.
func sanitizeMainUsage(response codex.RateLimitsResponse) *model.Window {
	for _, input := range []*codex.RateLimitWindow{
		response.RateLimits.Primary,
		response.RateLimits.Secondary,
	} {
		if input == nil || input.WindowDurationMins == nil || *input.WindowDurationMins != 10_080 {
			continue
		}
		return sanitizeWindow(input)
	}
	return nil
}

// sanitizeResetCredits forwards only the authoritative aggregate count. App
// Server may also return opaque credit rows; the client type intentionally
// cannot retain them and this boundary rejects nonsensical counts.
func sanitizeResetCredits(response codex.RateLimitsResponse) *int64 {
	if response.ResetCredits == nil || response.ResetCredits.AvailableCount == nil ||
		*response.ResetCredits.AvailableCount < 0 ||
		*response.ResetCredits.AvailableCount > model.MaxResetCreditsAvailable {
		return nil
	}
	available := *response.ResetCredits.AvailableCount
	return &available
}

func sanitizeLimit(input codex.RateLimitSnapshot, fallbackID string, index int) model.RateLimit {
	id := ""
	if input.LimitID != nil {
		id = safeText(*input.LimitID, 128, false)
	}
	if id == "" {
		id = safeText(fallbackID, 128, false)
	}
	if id == "" {
		id = fmt.Sprintf("bucket-%d", index+1)
	}
	limit := model.RateLimit{
		ID:                  id,
		Name:                safeOptional(input.LimitName, 128, true),
		PlanType:            safeOptional(input.PlanType, 128, true),
		Primary:             sanitizeWindow(input.Primary),
		Secondary:           sanitizeWindow(input.Secondary),
		SpendControlReached: cloneBool(input.SpendControlReached),
		ReachedType:         safeOptional(input.RateLimitReachedType, 128, true),
	}
	if input.Credits != nil {
		limit.Credits = &model.Credits{
			HasCredits: input.Credits.HasCredits,
			Unlimited:  input.Credits.Unlimited,
			Balance:    safeOptional(input.Credits.Balance, 64, true),
		}
	}
	if individual := input.IndividualLimit; individual != nil {
		limitText := safeText(individual.Limit, 64, false)
		usedText := safeText(individual.Used, 64, false)
		if limitText != "" && usedText != "" && validResetTimestamp(individual.ResetsAt) {
			limit.IndividualLimit = &model.IndividualLimit{
				Limit:            limitText,
				Used:             usedText,
				RemainingPercent: clampPercent(individual.RemainingPercent),
				ResetsAt:         individual.ResetsAt,
			}
		}
	}
	return limit
}

func sanitizeWindow(input *codex.RateLimitWindow) *model.Window {
	if input == nil {
		return nil
	}
	used := clampPercent(input.UsedPercent)
	window := &model.Window{UsedPercent: used, RemainingPercent: 100 - used}
	if input.WindowDurationMins != nil && *input.WindowDurationMins > 0 && *input.WindowDurationMins <= 5_256_000 {
		value := *input.WindowDurationMins
		window.WindowDurationMins = &value
	}
	if input.ResetsAt != nil && validResetTimestamp(*input.ResetsAt) {
		value := *input.ResetsAt
		window.ResetsAt = &value
	}
	return window
}

func rateLimitIsEmpty(value codex.RateLimitSnapshot) bool {
	return value.LimitID == nil && value.LimitName == nil && value.PlanType == nil &&
		value.Primary == nil && value.Secondary == nil && value.Credits == nil &&
		value.IndividualLimit == nil && value.SpendControlReached == nil &&
		value.RateLimitReachedType == nil
}

func safeOptional(value *string, maximum int, allowEmpty bool) *string {
	if value == nil {
		return nil
	}
	safe := safeText(*value, maximum, allowEmpty)
	if safe == "" && (!allowEmpty || *value != "") {
		return nil
	}
	return &safe
}

func safeText(value string, maximum int, allowEmpty bool) string {
	if len(value) > maximum || (!allowEmpty && strings.TrimSpace(value) == "") {
		return ""
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return value
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func uniqueLimitID(base string, index int, seen map[string]bool) string {
	if !seen[base] {
		seen[base] = true
		return base
	}
	for suffixNumber := 2; ; suffixNumber++ {
		suffix := fmt.Sprintf("-%d", suffixNumber)
		prefix := base
		if len(prefix)+len(suffix) > 128 {
			prefix = prefix[:128-len(suffix)]
		}
		candidate := prefix + suffix
		if candidate == suffix {
			candidate = fmt.Sprintf("bucket-%d%s", index+1, suffix)
		}
		if !seen[candidate] {
			seen[candidate] = true
			return candidate
		}
	}
}

func validResetTimestamp(value int64) bool {
	return value > 0 && value <= 32_503_680_000
}

func clampPercent(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
