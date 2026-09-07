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
		trimmedEmail := strings.TrimSpace(*email)
		trimmedPlan := strings.TrimSpace(*plan)
		account := &model.Account{Type: "chatgpt", Email: &trimmedEmail, PlanType: trimmedPlan}
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

func sanitizeLifetimeTokens(response codex.AccountUsageResponse) *int64 {
	if response.LifetimeTokens == nil || *response.LifetimeTokens < 0 ||
		*response.LifetimeTokens > model.MaxLifetimeTokens {
		return nil
	}
	value := *response.LifetimeTokens
	return &value
}

func sanitizeRecentThreads(response codex.ThreadListResponse) []model.RecentThread {
	bySession := make(map[string]model.RecentThread, min(len(response.Threads), model.MaxRecentThreads))
	order := make([]string, 0, min(len(response.Threads), model.MaxRecentThreads))
	for _, thread := range response.Threads {
		if !validInteractiveThread(thread) {
			continue
		}
		sessionID := safeSessionID(thread.SessionID)
		name := ""
		if thread.Name != nil {
			name = *thread.Name
		}
		candidate := model.RecentThread{
			ThreadID:  sessionID,
			TaskName:  model.SanitizeTaskName(name),
			CreatedAt: thread.CreatedAt,
			UpdatedAt: thread.UpdatedAt,
		}
		if current, exists := bySession[sessionID]; exists {
			candidate = mergeRecentThread(current, candidate)
		} else {
			order = append(order, sessionID)
		}
		bySession[sessionID] = candidate
	}
	result := make([]model.RecentThread, 0, len(bySession))
	for _, sessionID := range order {
		result = append(result, bySession[sessionID])
		if len(result) == model.MaxRecentThreads {
			break
		}
	}
	return result
}

func sanitizeRuntimeThreads(response codex.ThreadListResponse) []model.RuntimeThread {
	bySession := make(map[string]model.RuntimeThread, min(len(response.Threads), model.MaxRuntimeThreads))
	order := make([]string, 0, min(len(response.Threads), model.MaxRuntimeThreads))
	for _, thread := range response.Threads {
		// Only top-level interactive chats become dashboard rows. Spawned
		// agent threads share the parent's hook session and would otherwise
		// multiply one chat into several identical entries.
		if !validInteractiveThread(thread) {
			continue
		}
		running := false
		switch thread.Status.Type {
		case "active":
			running = true
		case "idle":
			// Loaded but no turn is executing.
		default:
			// notLoaded, systemError, and unknown future values are not
			// current chat activity.
			continue
		}
		sessionID := safeSessionID(thread.SessionID)
		name := ""
		if thread.Name != nil {
			name = *thread.Name
		}
		candidate := model.RuntimeThread{
			ThreadID:  sessionID,
			TaskName:  model.SanitizeTaskName(name),
			CreatedAt: thread.CreatedAt,
			UpdatedAt: thread.UpdatedAt,
			Running:   running,
		}
		if current, exists := bySession[sessionID]; exists {
			candidate = mergeRuntimeThread(current, candidate)
		} else {
			order = append(order, sessionID)
		}
		bySession[sessionID] = candidate
	}
	result := make([]model.RuntimeThread, 0, len(bySession))
	for _, sessionID := range order {
		result = append(result, bySession[sessionID])
		if len(result) == model.MaxRuntimeThreads {
			break
		}
	}
	return result
}

func validInteractiveThread(thread codex.Thread) bool {
	return thread.ParentThreadID == nil && thread.Source.Allowed() &&
		safeSessionID(thread.SessionID) != "" && thread.CreatedAt >= 0 && thread.UpdatedAt >= 0 &&
		thread.CreatedAt <= 32_503_680_000 && thread.UpdatedAt <= 32_503_680_000 &&
		(thread.CreatedAt == 0 || thread.UpdatedAt == 0 || thread.UpdatedAt >= thread.CreatedAt)
}

func safeSessionID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed != value {
		return ""
	}
	return safeText(value, 128, false)
}

func mergeRecentThread(current, candidate model.RecentThread) model.RecentThread {
	createdAt := earliestNonzero(current.CreatedAt, candidate.CreatedAt)
	updatedAt := latestNonzero(current.UpdatedAt, candidate.UpdatedAt)
	taskName := freshestTaskName(current.TaskName, current.UpdatedAt, candidate.TaskName, candidate.UpdatedAt)
	return model.RecentThread{
		ThreadID: current.ThreadID, TaskName: taskName,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}

func mergeRuntimeThread(current, candidate model.RuntimeThread) model.RuntimeThread {
	createdAt := earliestNonzero(current.CreatedAt, candidate.CreatedAt)
	updatedAt := latestNonzero(current.UpdatedAt, candidate.UpdatedAt)
	taskName := freshestTaskName(current.TaskName, current.UpdatedAt, candidate.TaskName, candidate.UpdatedAt)
	return model.RuntimeThread{
		ThreadID: current.ThreadID, TaskName: taskName,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
		Running: current.Running || candidate.Running,
	}
}

func earliestNonzero(left, right int64) int64 {
	if left == 0 {
		return right
	}
	if right == 0 || left < right {
		return left
	}
	return right
}

func latestNonzero(left, right int64) int64 {
	if right > left {
		return right
	}
	return left
}

func freshestTaskName(current string, currentUpdatedAt int64, candidate string, candidateUpdatedAt int64) string {
	if candidateUpdatedAt > currentUpdatedAt ||
		(candidateUpdatedAt == currentUpdatedAt && candidate != "Codex task" &&
			(current == "Codex task" || candidate < current)) {
		return candidate
	}
	return current
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
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
			unicode.Is(unicode.Cs, r) || unicode.Is(unicode.Co, r) {
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
