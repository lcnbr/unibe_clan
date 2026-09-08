package hub

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	usagehistory "codex-usage-dashboard/internal/history"
	"codex-usage-dashboard/internal/model"
)

const runtimeInventoryGrace = 90 * time.Second

type Identity struct {
	Username      string
	UID           uint32
	ExpectedEmail string
}

// ActivityRef is private hook state supplied to Hub. SessionID is used only
// for an in-memory lookup against collector metadata and is never returned by
// Status, SSE, history, or logs.
type ActivityRef struct {
	Username  string
	SessionID string
	StartedAt time.Time
	UpdatedAt time.Time
	Running   bool
}

type entry struct {
	identity Identity
	current  model.Snapshot

	lastSeenAt time.Time
	lastGood   *model.Snapshot
	lastGoodAt *time.Time
	membership string

	lifetimeTokens *int64
	lifetimeAt     time.Time
	codexVersion   string
	codexVersionAt time.Time
	recentThreads  map[string]model.RecentThread
	runtimeThreads map[string]model.RuntimeThread
	// runtimeInventoryAt is dashboard-owned receipt time for the most recent
	// successful control-socket inventory. A brief optional read failure may
	// retain that private inventory, but it is never treated as known beyond
	// runtimeInventoryGrace.
	runtimeInventoryAt time.Time
	// runtimeQuarantine contains private App Server thread IDs which were
	// present across an account boundary. App Server does not identify the
	// account that owns a thread, so these IDs stay hidden until a complete
	// successful observation proves that they disappeared.
	runtimeQuarantine        map[string]struct{}
	runtimeQuarantinePending bool
	accountVersion           uint64
}

type historySignature struct {
	ObservedAt  time.Time
	UsedPercent int
	ResetsAt    int64
	HasWindow   bool
}

type historySourceState struct {
	source    historySourceIdentity
	signature historySignature
}

type historySourceIdentity struct {
	username       string
	accountVersion uint64
}

type canonicalObservation struct {
	snapshot model.Snapshot
	source   historySourceIdentity
}

type historyUpdate struct {
	snapshot   model.Snapshot
	rebaseline bool
}

type Hub struct {
	applyMu sync.Mutex
	mu      sync.RWMutex

	order           []string
	byUID           map[uint32]string
	entries         map[string]entry
	expectedAnchors map[string]string
	expectedEmails  map[string]string
	staleAfter      time.Duration
	now             func() time.Time
	demo            bool
	revision        uint64
	nextSubID       uint64
	subs            map[uint64]chan model.StatusResponse
	history         *usagehistory.Tracker
	historyObserved map[string]historySourceState
	activitySource  func() []ActivityRef
}

func New(identities []Identity, staleAfter time.Duration) (*Hub, error) {
	if staleAfter <= 0 {
		return nil, errors.New("stale-after must be positive")
	}
	h := &Hub{
		byUID:           make(map[uint32]string, len(identities)),
		entries:         make(map[string]entry, len(identities)),
		expectedAnchors: make(map[string]string),
		expectedEmails:  make(map[string]string),
		staleAfter:      staleAfter,
		now:             time.Now,
		subs:            make(map[uint64]chan model.StatusResponse),
		historyObserved: make(map[string]historySourceState),
	}
	seenNames := make(map[string]bool, len(identities))
	for _, identity := range identities {
		if err := validateIdentity(identity); err != nil {
			return nil, err
		}
		if seenNames[identity.Username] {
			return nil, fmt.Errorf("duplicate username %q", identity.Username)
		}
		if prior, ok := h.byUID[identity.UID]; ok {
			return nil, fmt.Errorf("UID %d belongs to both %q and %q", identity.UID, prior, identity.Username)
		}
		if identity.ExpectedEmail != "" {
			identity.ExpectedEmail = strings.ToLower(strings.TrimSpace(identity.ExpectedEmail))
			accountKey := model.AccountKey(identity.ExpectedEmail)
			if prior, ok := h.expectedAnchors[accountKey]; ok {
				return nil, fmt.Errorf("expected account belongs to both %q and %q", prior, identity.Username)
			}
			h.expectedAnchors[accountKey] = identity.Username
			h.expectedEmails[accountKey] = identity.ExpectedEmail
		}
		seenNames[identity.Username] = true
		h.byUID[identity.UID] = identity.Username
		h.order = append(h.order, identity.Username)
		h.entries[identity.Username] = entry{
			identity: identity,
			current: model.Snapshot{
				SchemaVersion: model.SchemaVersion,
				Username:      identity.Username,
				State:         model.StateUnavailable,
				Limits:        []model.RateLimit{},
				ErrorCategory: model.ErrorAwaitingCollector,
			},
			recentThreads:     make(map[string]model.RecentThread),
			runtimeThreads:    make(map[string]model.RuntimeThread),
			runtimeQuarantine: make(map[string]struct{}),
		}
	}
	sort.Strings(h.order)
	return h, nil
}

func validateIdentity(identity Identity) error {
	if err := validateSafeText("identity username", identity.Username, 64, false); err != nil {
		return err
	}
	if identity.ExpectedEmail == "" {
		return nil
	}
	email := strings.TrimSpace(identity.ExpectedEmail)
	if err := validateSafeText("expected anchor email", email, 320, false); err != nil {
		return err
	}
	if strings.Count(email, "@") != 1 || strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@") {
		return errors.New("expected anchor email is invalid")
	}
	return nil
}

func validateSafeText(name, value string, maximum int, allowEmpty bool) error {
	if !allowEmpty && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > maximum {
		return fmt.Errorf("%s is too long", name)
	}
	for _, char := range value {
		if unicode.IsControl(char) || unicode.Is(unicode.Cf, char) ||
			unicode.Is(unicode.Cs, char) || unicode.Is(unicode.Co, char) {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func (h *Hub) SetHistory(tracker *usagehistory.Tracker) {
	h.applyMu.Lock()
	defer h.applyMu.Unlock()
	h.mu.Lock()
	h.history = tracker
	h.historyObserved = make(map[string]historySourceState)
	var updates []historyUpdate
	if tracker != nil {
		_, canonicals := h.aggregateLocked(h.now().UTC())
		updates = h.newHistoryUpdatesLocked(canonicals)
	}
	h.mu.Unlock()
	for _, update := range updates {
		applyHistoryUpdate(tracker, update)
	}
}

func (h *Hub) SetActivitySource(source func() []ActivityRef) {
	h.mu.Lock()
	h.activitySource = source
	h.revision++
	h.broadcastLocked(h.statusLocked(h.now().UTC()))
	h.mu.Unlock()
}

func (h *Hub) activitySessionIDsLocked(username string) map[string]struct{} {
	result := make(map[string]struct{})
	if h.activitySource == nil {
		return result
	}
	for _, activity := range h.activitySource() {
		if activity.Username == username && activity.SessionID != "" {
			result[activity.SessionID] = struct{}{}
		}
	}
	return result
}

// Refresh publishes external in-memory changes such as hook activity without
// accepting or exposing the hook payload itself.
func (h *Hub) Refresh() {
	h.mu.Lock()
	h.revision++
	h.broadcastLocked(h.statusLocked(h.now().UTC()))
	h.mu.Unlock()
}

func (h *Hub) SetDemo(enabled bool) {
	h.mu.Lock()
	if h.demo != enabled {
		h.demo = enabled
		h.revision++
		h.broadcastLocked(h.statusLocked(h.now().UTC()))
	}
	h.mu.Unlock()
}

func (h *Hub) Apply(uid uint32, incoming model.Snapshot) error {
	h.applyMu.Lock()
	defer h.applyMu.Unlock()

	h.mu.RLock()
	username, ok := h.byUID[uid]
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("untrusted collector UID %d", uid)
	}
	if incoming.Username != username {
		return fmt.Errorf("collector UID %d cannot submit as %q", uid, incoming.Username)
	}
	if err := incoming.Validate(); err != nil {
		return fmt.Errorf("invalid snapshot: %w", err)
	}
	incoming = cloneSnapshot(incoming)

	h.mu.Lock()
	now := h.now().UTC()
	stored := h.entries[username]
	if !stored.current.ObservedAt.IsZero() &&
		!incoming.ObservedAt.After(stored.current.ObservedAt) {
		// Ingest connections are handled concurrently, and an old collector may
		// briefly overlap its replacement. Acknowledge duplicate or delayed
		// snapshots without applying them so the publisher does not retry forever.
		// This ordering covers every state: a newer unavailable observation keeps
		// last-good account data but cannot be masked by an older OK snapshot, and
		// only a newer signed-out/API-key observation may clear membership.
		h.mu.Unlock()
		return nil
	}
	if incoming.CodexVersion != "" && incoming.CodexVersionObservedAt != nil &&
		(stored.codexVersionAt.IsZero() || incoming.CodexVersionObservedAt.After(stored.codexVersionAt)) {
		// The CLI version belongs to the Linux user, not an account. Keep the
		// newest exact process observation across account changes, sign-out, and
		// transient process-discovery failures.
		stored.codexVersion = incoming.CodexVersion
		stored.codexVersionAt = incoming.CodexVersionObservedAt.UTC()
	}
	priorAccount := snapshotAccountKeyFromPointer(stored.lastGood)
	newAccount := ""
	if incoming.State == model.StateOK {
		newAccount = snapshotAccountKey(incoming)
	}
	consumerSwitch := stored.identity.ExpectedEmail == "" && priorAccount != "" &&
		newAccount != "" && priorAccount != newAccount
	activityIDs := make(map[string]struct{})
	if stored.identity.ExpectedEmail == "" &&
		(consumerSwitch || (incoming.State == model.StateOK && incoming.RuntimeThreadsRead)) {
		activityIDs = h.activitySessionIDsLocked(username)
	}
	// Runtime thread identifiers are account- and process-local metadata. Keep
	// them only in private, in-memory indexes. Retain the last successful same-
	// account inventory across only a brief optional control read failure. A
	// confirmed successful observation, including an empty one, replaces it
	// immediately; non-OK observations and account switches clear it.
	retainRuntime := stored.identity.ExpectedEmail == "" &&
		incoming.State == model.StateOK && !consumerSwitch && !incoming.RuntimeThreadsRead &&
		runtimeInventoryRecent(stored, now)
	if !retainRuntime {
		stored.runtimeThreads = make(map[string]model.RuntimeThread)
		stored.runtimeInventoryAt = time.Time{}
	}
	// A successful read can retire quarantined IDs which disappeared. A read at
	// an account boundary cannot tell which account owns an already loaded
	// thread, so every ID in that observation is quarantined. If that boundary
	// read fails, quarantine the first later complete observation instead.
	if stored.identity.ExpectedEmail == "" {
		if incoming.State == model.StateOK && incoming.RuntimeThreadsRead {
			observed := make(map[string]model.RuntimeThread, len(incoming.RuntimeThreads))
			for _, thread := range incoming.RuntimeThreads {
				observed[thread.ThreadID] = thread
			}

			// Only a complete successful observation proves absence. Retain
			// quarantined IDs which are still present in either independent
			// activity source and retire the rest.
			nextQuarantine := make(map[string]struct{}, len(stored.runtimeQuarantine))
			for sessionID := range stored.runtimeQuarantine {
				_, presentInRuntime := observed[sessionID]
				_, presentInHooks := activityIDs[sessionID]
				if presentInRuntime || presentInHooks {
					nextQuarantine[sessionID] = struct{}{}
				}
			}
			if consumerSwitch || stored.runtimeQuarantinePending {
				for sessionID := range observed {
					nextQuarantine[sessionID] = struct{}{}
				}
				stored.runtimeQuarantinePending = false
			}
			if consumerSwitch {
				// Hook-only sessions already running at the boundary are just as
				// ambiguous as loaded App Server sessions. New hook IDs observed
				// after this atomic switch are not added here.
				for sessionID := range activityIDs {
					nextQuarantine[sessionID] = struct{}{}
				}
			}
			stored.runtimeQuarantine = nextQuarantine
			for sessionID, thread := range observed {
				if _, quarantined := stored.runtimeQuarantine[sessionID]; !quarantined {
					stored.runtimeThreads[sessionID] = thread
				}
			}
			if !consumerSwitch {
				stored.runtimeInventoryAt = now
			}
		} else if consumerSwitch {
			// Optional runtime collection failed at the boundary. Until one
			// complete observation arrives, no observed ID can safely be
			// assigned to the new account.
			stored.runtimeQuarantinePending = true
			for sessionID := range activityIDs {
				stored.runtimeQuarantine[sessionID] = struct{}{}
			}
		}
	}
	// Do not let opaque runtime IDs flow into last-good/canonical snapshots,
	// which are later passed to history detection. Their only lifetime is the
	// private map above.
	incoming.RuntimeThreads = nil
	incoming.RuntimeThreadsRead = false
	stored.current = incoming
	stored.lastSeenAt = now
	switch incoming.State {
	case model.StateOK:
		newMembership := newAccount
		if priorAccount != "" && priorAccount != newMembership {
			stored.accountVersion++
			// Optional reads can fail independently. Never carry an old
			// account's lifetime total or thread-name index into the new one.
			stored.lifetimeTokens = nil
			stored.lifetimeAt = time.Time{}
			stored.recentThreads = make(map[string]model.RecentThread)
		}
		lastGood := cloneSnapshot(incoming)
		stored.lastGood = &lastGood
		goodAt := now
		stored.lastGoodAt = &goodAt
		if stored.identity.ExpectedEmail == "" {
			stored.membership = newMembership
		}
		if incoming.LifetimeTokensRead && incoming.LifetimeTokens != nil {
			stored.lifetimeTokens = cloneInt64(incoming.LifetimeTokens)
			stored.lifetimeAt = incoming.ObservedAt
		}
		if incoming.RecentThreadsRead {
			stored.recentThreads = make(map[string]model.RecentThread, len(incoming.RecentThreads))
			for _, thread := range incoming.RecentThreads {
				stored.recentThreads[thread.ThreadID] = thread
			}
		}
	case model.StateSignedOut, model.StateAPIKey:
		stored.accountVersion++
		if stored.identity.ExpectedEmail == "" {
			stored.membership = ""
		}
		stored.lifetimeTokens = nil
		stored.lifetimeAt = time.Time{}
		stored.recentThreads = make(map[string]model.RecentThread)
	case model.StateUnavailable:
		// Retain membership and last-good caches across transient failures.
	}
	h.entries[username] = stored
	h.revision++
	response, canonicals := h.aggregateLocked(now)
	h.broadcastLocked(response)
	tracker := h.history
	var updates []historyUpdate
	if tracker != nil {
		updates = h.newHistoryUpdatesLocked(canonicals)
	}
	h.mu.Unlock()

	if tracker != nil {
		for _, update := range updates {
			applyHistoryUpdate(tracker, update)
		}
	}
	return nil
}

func (h *Hub) newHistoryUpdatesLocked(canonicals map[string]canonicalObservation) []historyUpdate {
	updates := make([]historyUpdate, 0, len(canonicals))
	for accountKey, canonical := range canonicals {
		signature := signatureForHistory(canonical.snapshot)
		prior, observed := h.historyObserved[accountKey]
		if observed && prior.source == canonical.source && prior.signature == signature {
			continue
		}
		h.historyObserved[accountKey] = historySourceState{source: canonical.source, signature: signature}
		updates = append(updates, historyUpdate{
			snapshot: cloneSnapshot(canonical.snapshot),
			// Source identity is intentionally never persisted. Rebaseline the
			// first post-start observation as well as proven in-memory source
			// handoffs. Rebaseline itself preserves unambiguous account-level reset
			// transitions across these boundaries.
			rebaseline: !observed || prior.source != canonical.source,
		})
	}
	sort.Slice(updates, func(i, j int) bool {
		return snapshotAccountKey(updates[i].snapshot) < snapshotAccountKey(updates[j].snapshot)
	})
	return updates
}

func applyHistoryUpdate(tracker *usagehistory.Tracker, update historyUpdate) {
	if update.rebaseline {
		tracker.Rebaseline(update.snapshot)
		return
	}
	tracker.Observe(update.snapshot)
}

func signatureForHistory(snapshot model.Snapshot) historySignature {
	signature := historySignature{ObservedAt: snapshot.ObservedAt}
	if snapshot.MainUsage != nil && snapshot.MainUsage.ResetsAt != nil {
		signature.HasWindow = true
		signature.UsedPercent = snapshot.MainUsage.UsedPercent
		signature.ResetsAt = *snapshot.MainUsage.ResetsAt
	}
	return signature
}

func (h *Hub) Status() model.StatusResponse {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.statusLocked(h.now().UTC())
}

func (h *Hub) statusLocked(now time.Time) model.StatusResponse {
	response, _ := h.aggregateLocked(now)
	return response
}

type sourceCandidate struct {
	username string
	entry    entry
	snapshot model.Snapshot
	anchor   bool
	stale    bool
	fresh    bool
}

func (h *Hub) aggregateLocked(now time.Time) (model.StatusResponse, map[string]canonicalObservation) {
	accountKeys := make(map[string]bool, len(h.expectedAnchors)+len(h.entries))
	for accountKey := range h.expectedAnchors {
		accountKeys[accountKey] = true
	}
	for _, stored := range h.entries {
		if stored.identity.ExpectedEmail == "" && stored.membership != "" {
			accountKeys[stored.membership] = true
		}
	}
	activities := []ActivityRef{}
	if h.activitySource != nil {
		activities = h.activitySource()
	}
	byAccount := make(map[string]model.AccountStatus, len(accountKeys))
	canonicals := make(map[string]canonicalObservation, len(accountKeys))
	for accountKey := range accountKeys {
		status, canonical := h.accountStatusLocked(accountKey, now, activities)
		byAccount[accountKey] = status
		if canonical != nil {
			canonicals[accountKey] = *canonical
		}
	}

	accounts := make([]model.AccountStatus, 0, len(byAccount))
	for _, status := range byAccount {
		accounts = append(accounts, status)
	}
	sort.Slice(accounts, func(i, j int) bool {
		left := accountDisplayEmail(accounts[i])
		right := accountDisplayEmail(accounts[j])
		if left != right {
			return left < right
		}
		return accounts[i].AccountKey < accounts[j].AccountKey
	})

	unassigned := make([]model.UserStatus, 0)
	for _, username := range h.order {
		stored := h.entries[username]
		if stored.identity.ExpectedEmail == "" && stored.membership == "" {
			unassigned = append(unassigned, userStatus(stored, now, h.staleAfter))
		}
	}
	sort.Slice(unassigned, func(i, j int) bool { return unassigned[i].Username < unassigned[j].Username })
	return model.StatusResponse{
		SchemaVersion:   model.SchemaVersion,
		Revision:        h.revision,
		GeneratedAt:     now,
		Demo:            h.demo,
		Accounts:        accounts,
		UnassignedUsers: unassigned,
	}, canonicals
}

func (h *Hub) accountStatusLocked(accountKey string, now time.Time, activities []ActivityRef) (model.AccountStatus, *canonicalObservation) {
	users := make([]model.UserStatus, 0)
	candidates := make([]sourceCandidate, 0)
	freshSources := make([]model.Snapshot, 0)
	for _, username := range h.order {
		stored := h.entries[username]
		role := roleFor(stored.identity)
		belongs := false
		correctAnchor := false
		if role == model.UserRoleAnchor {
			belongs = model.AccountKey(stored.identity.ExpectedEmail) == accountKey
			correctAnchor = belongs && snapshotAccountKey(stored.current) == accountKey
		} else {
			belongs = stored.membership == accountKey
		}
		if !belongs {
			continue
		}
		users = append(users, userStatus(stored, now, h.staleAfter))
		stale := entryStale(stored, now, h.staleAfter)
		if stored.lastGood == nil {
			continue
		}
		if role == model.UserRoleAnchor {
			if correctAnchor && stored.current.State == model.StateOK && !stale {
				candidates = append(candidates, sourceCandidate{
					username: username, entry: stored, snapshot: cloneSnapshot(*stored.lastGood), anchor: true, fresh: true,
				})
				freshSources = append(freshSources, cloneSnapshot(*stored.lastGood))
			}
			continue
		}
		fresh := stored.current.State == model.StateOK && !stale
		candidates = append(candidates, sourceCandidate{
			username: username, entry: stored, snapshot: cloneSnapshot(*stored.lastGood), stale: stale, fresh: fresh,
		})
		if fresh {
			freshSources = append(freshSources, cloneSnapshot(*stored.lastGood))
		}
	}
	sort.Slice(users, func(i, j int) bool {
		if users[i].Role != users[j].Role {
			return users[i].Role == model.UserRoleAnchor
		}
		return users[i].Username < users[j].Username
	})
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].anchor != candidates[j].anchor {
			return candidates[i].anchor
		}
		if candidates[i].stale != candidates[j].stale {
			return !candidates[i].stale
		}
		if !candidates[i].snapshot.ObservedAt.Equal(candidates[j].snapshot.ObservedAt) {
			return candidates[i].snapshot.ObservedAt.After(candidates[j].snapshot.ObservedAt)
		}
		return candidates[i].username < candidates[j].username
	})

	status := model.AccountStatus{
		AccountKey:  accountKey,
		State:       model.StateUnavailable,
		Limits:      []model.RateLimit{},
		Users:       users,
		ActiveChats: h.activeChatsLocked(accountKey, activities, now),
		Stale:       true,
	}
	if expectedEmail := h.expectedEmails[accountKey]; expectedEmail != "" {
		email := expectedEmail
		status.Account = &model.Account{Type: "chatgpt", Email: &email}
		anchor := h.entries[h.expectedAnchors[accountKey]]
		status.AnchorHealth = anchorHealth(anchor, accountKey, now, h.staleAfter)
		status.State = stateWithoutCanonical(anchor, status.AnchorHealth)
		status.LastSeenAt = anchor.lastSeenAt
		status.LastGoodAt = cloneTime(anchor.lastGoodAt)
		status.Stale = status.AnchorHealth == model.AnchorHealthStale
	}

	var canonical *canonicalObservation
	if len(candidates) != 0 {
		selected := candidates[0]
		status.State = selected.entry.current.State
		status.Account = cloneAccount(selected.snapshot.Account)
		if expectedEmail := h.expectedEmails[accountKey]; expectedEmail != "" {
			status.Account.Email = cloneString(&expectedEmail)
		}
		status.MainUsage = cloneWindow(selected.snapshot.MainUsage)
		status.ResetCreditsAvailable = cloneInt64(selected.snapshot.ResetCreditsAvailable)
		status.Limits = cloneLimits(selected.snapshot.Limits)
		status.ObservedAt = selected.snapshot.ObservedAt
		status.LastSeenAt = selected.entry.lastSeenAt
		status.LastGoodAt = cloneTime(selected.entry.lastGoodAt)
		status.Stale = selected.stale
	}
	status.LifetimeTokens = h.selectLifetimeTokensLocked(accountKey)
	status.SourceConflict = snapshotsConflict(freshSources)
	if selected, ok := h.selectHistoryCandidateLocked(accountKey, candidates, now); ok {
		// A correctly mapped anchor is authoritative even while a consumer is
		// reporting conflicting data. Without an anchor, wait for duplicate
		// consumers to converge so their disagreement cannot create false reset
		// events or adjustments.
		if !status.SourceConflict || selected.anchor {
			canonical = &canonicalObservation{
				snapshot: cloneSnapshot(selected.snapshot),
				source: historySourceIdentity{
					username:       selected.username,
					accountVersion: selected.entry.accountVersion,
				},
			}
		}
	}
	return status, canonical
}

// selectHistoryCandidateLocked keeps account history on one healthy source
// even though the display source is intentionally the freshest collector.
// This prevents out-of-phase collectors from alternating the history source on
// every poll. A brief failure of the selected source pauses history until its
// last good observation becomes stale; recovery then continues the same source
// identity instead of rebasing it. Explicit sign-out/account changes increment
// accountVersion and therefore end this grace period immediately.
func (h *Hub) selectHistoryCandidateLocked(accountKey string, candidates []sourceCandidate, now time.Time) (sourceCandidate, bool) {
	prior, hasPrior := h.historyObserved[accountKey]
	// A correctly mapped fresh anchor is authoritative. Upgrade to it once,
	// then the normal prior-source path below keeps it sticky.
	for _, candidate := range candidates {
		if candidate.fresh && candidate.anchor {
			return candidate, true
		}
	}
	if hasPrior {
		if stored, ok := h.entries[prior.source.username]; ok &&
			stored.accountVersion == prior.source.accountVersion &&
			snapshotAccountKeyFromPointer(stored.lastGood) == accountKey {
			for _, candidate := range candidates {
				if candidate.fresh && candidate.username == prior.source.username &&
					candidate.entry.accountVersion == prior.source.accountVersion {
					return candidate, true
				}
			}
			if stored.lastGoodAt != nil && (now.Before(*stored.lastGoodAt) ||
				now.Sub(*stored.lastGoodAt) <= h.staleAfter) {
				return sourceCandidate{}, false
			}
		}
	}

	var selected *sourceCandidate
	for index := range candidates {
		candidate := candidates[index]
		if !candidate.fresh {
			continue
		}
		if selected == nil || (candidate.anchor && !selected.anchor) ||
			(candidate.anchor == selected.anchor && candidate.username < selected.username) {
			copy := candidate
			selected = &copy
		}
	}
	if selected == nil {
		return sourceCandidate{}, false
	}
	return *selected, true
}

func stateWithoutCanonical(anchor entry, health model.AnchorHealth) model.State {
	if health == model.AnchorHealthSignedOut {
		return model.StateSignedOut
	}
	if health == model.AnchorHealthWrongAccount {
		return model.StateUnavailable
	}
	if anchor.current.State == model.StateAPIKey {
		return model.StateSignedOut
	}
	return model.StateUnavailable
}

func (h *Hub) selectLifetimeTokensLocked(accountKey string) *int64 {
	type observation struct {
		value    int64
		at       time.Time
		username string
	}
	var selected *observation
	for _, username := range h.order {
		stored := h.entries[username]
		if stored.lifetimeTokens == nil {
			continue
		}
		belongs := stored.membership == accountKey
		if stored.identity.ExpectedEmail != "" {
			belongs = model.AccountKey(stored.identity.ExpectedEmail) == accountKey &&
				snapshotAccountKeyFromPointer(stored.lastGood) == accountKey &&
				stored.current.State != model.StateSignedOut && stored.current.State != model.StateAPIKey
		}
		if !belongs {
			continue
		}
		candidate := observation{value: *stored.lifetimeTokens, at: stored.lifetimeAt, username: username}
		if selected == nil || candidate.at.After(selected.at) ||
			(candidate.at.Equal(selected.at) && candidate.username < selected.username) {
			copy := candidate
			selected = &copy
		}
	}
	if selected == nil {
		return nil
	}
	value := selected.value
	return &value
}

type privateChatKey struct {
	username string
	session  string
}

func (h *Hub) activeChatsLocked(accountKey string, activities []ActivityRef, now time.Time) []model.ActiveChat {
	// projected is keyed by private identifiers, but its values deliberately
	// contain no identifier. Only the values leave this function.
	projected := make(map[privateChatKey]model.ActiveChat)
	for _, username := range h.order {
		stored := h.entries[username]
		if stored.identity.ExpectedEmail != "" || stored.membership != accountKey ||
			stored.current.State != model.StateOK || entryStale(stored, now, h.staleAfter) ||
			!runtimeInventoryRecent(stored, now) {
			continue
		}
		fallback := stored.current.ObservedAt
		if fallback.IsZero() {
			fallback = stored.lastSeenAt
		}
		for sessionID, thread := range stored.runtimeThreads {
			if sessionID == "" {
				continue
			}
			startedAt, updatedAt := runtimeChatTimes(thread, fallback, now)
			projected[privateChatKey{username: username, session: sessionID}] = model.ActiveChat{
				TaskName:  model.SanitizeTaskName(thread.TaskName),
				Username:  username,
				StartedAt: startedAt,
				UpdatedAt: updatedAt,
				Running:   thread.Running,
			}
		}
	}

	for _, activity := range activities {
		stored, ok := h.entries[activity.Username]
		if !ok || activity.SessionID == "" || stored.identity.ExpectedEmail != "" || stored.membership != accountKey {
			continue
		}
		if stored.runtimeQuarantinePending {
			// A failed boundary inventory means every currently loaded App
			// Server session is ambiguous. Hook state cannot establish account
			// ownership either, so keep it hidden until the next complete read.
			continue
		}
		if _, quarantined := stored.runtimeQuarantine[activity.SessionID]; quarantined {
			continue
		}
		key := privateChatKey{username: activity.Username, session: activity.SessionID}
		startedAt, updatedAt := normalizeChatTimes(activity.StartedAt, activity.UpdatedAt, now)
		if chat, exists := projected[key]; exists {
			chat.StartedAt = earlierTime(chat.StartedAt, startedAt)
			chat.UpdatedAt = laterTime(chat.UpdatedAt, updatedAt)
			chat.Running = chat.Running || activity.Running
			projected[key] = chat
			continue
		}
		name := "Active Codex task"
		if thread, found := stored.recentThreads[activity.SessionID]; found {
			name = thread.TaskName
		}
		projected[key] = model.ActiveChat{
			TaskName:  model.SanitizeTaskName(name),
			Username:  activity.Username,
			StartedAt: startedAt,
			UpdatedAt: updatedAt,
			Running:   activity.Running,
		}
	}

	result := make([]model.ActiveChat, 0, len(projected))
	for _, chat := range projected {
		// Idle App Server sessions remain private inputs for lifecycle merging,
		// but the public API intentionally exposes only work that is running
		// now. This keeps every consumer of activeChats consistent with the UI.
		if !chat.Running {
			continue
		}
		result = append(result, chat)
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].UpdatedAt.After(result[j].UpdatedAt)
		}
		if result[i].Username != result[j].Username {
			return result[i].Username < result[j].Username
		}
		return result[i].TaskName < result[j].TaskName
	})
	return result
}

func runtimeChatTimes(thread model.RuntimeThread, fallback, now time.Time) (time.Time, time.Time) {
	var startedAt, updatedAt time.Time
	if thread.CreatedAt != 0 {
		startedAt = time.Unix(thread.CreatedAt, 0).UTC()
	}
	if thread.UpdatedAt != 0 {
		updatedAt = time.Unix(thread.UpdatedAt, 0).UTC()
	}
	if fallback.IsZero() {
		fallback = now
	}
	return normalizeChatTimes(startedAt, updatedAt, fallback)
}

func normalizeChatTimes(startedAt, updatedAt, fallback time.Time) (time.Time, time.Time) {
	fallback = fallback.UTC()
	if fallback.IsZero() {
		fallback = time.Unix(0, 0).UTC()
	}
	if startedAt.IsZero() && updatedAt.IsZero() {
		return fallback, fallback
	}
	if startedAt.IsZero() {
		startedAt = updatedAt
	}
	if updatedAt.IsZero() {
		updatedAt = startedAt
	}
	startedAt = startedAt.UTC()
	updatedAt = updatedAt.UTC()
	if updatedAt.Before(startedAt) {
		startedAt, updatedAt = updatedAt, startedAt
	}
	return startedAt, updatedAt
}

func earlierTime(left, right time.Time) time.Time {
	if left.IsZero() || (!right.IsZero() && right.Before(left)) {
		return right
	}
	return left
}

func laterTime(left, right time.Time) time.Time {
	if left.IsZero() || right.After(left) {
		return right
	}
	return left
}

const (
	quotaPercentTolerance = 5
	quotaResetTolerance   = int64(5 * time.Minute / time.Second)
)

func snapshotsConflict(snapshots []model.Snapshot) bool {
	for left := 0; left < len(snapshots); left++ {
		for right := left + 1; right < len(snapshots); right++ {
			if !quotaSnapshotsEquivalent(snapshots[left], snapshots[right]) {
				return true
			}
		}
	}
	return false
}

func quotaSnapshotsEquivalent(left, right model.Snapshot) bool {
	if !accountsEquivalent(left.Account, right.Account) ||
		!windowsEquivalent(left.MainUsage, right.MainUsage) ||
		!int64PointersEqual(left.ResetCreditsAvailable, right.ResetCreditsAvailable) ||
		len(left.Limits) != len(right.Limits) {
		return false
	}
	rightByID := make(map[string]model.RateLimit, len(right.Limits))
	for _, limit := range right.Limits {
		rightByID[limit.ID] = limit
	}
	if len(rightByID) != len(right.Limits) {
		return false
	}
	for _, limit := range left.Limits {
		other, ok := rightByID[limit.ID]
		if !ok || !rateLimitsEquivalent(limit, other) {
			return false
		}
	}
	return true
}

func accountsEquivalent(left, right *model.Account) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Type == right.Type && normalizedDisplayText(left.PlanType) == normalizedDisplayText(right.PlanType)
}

func rateLimitsEquivalent(left, right model.RateLimit) bool {
	return left.ID == right.ID &&
		displayStringPointersEqual(left.Name, right.Name) &&
		displayStringPointersEqual(left.PlanType, right.PlanType) &&
		windowsEquivalent(left.Primary, right.Primary) &&
		windowsEquivalent(left.Secondary, right.Secondary) &&
		creditsEquivalent(left.Credits, right.Credits) &&
		individualLimitsEquivalent(left.IndividualLimit, right.IndividualLimit) &&
		boolPointersEqual(left.SpendControlReached, right.SpendControlReached) &&
		stringPointersEqual(left.ReachedType, right.ReachedType)
}

func windowsEquivalent(left, right *model.Window) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return withinIntTolerance(left.UsedPercent, right.UsedPercent, quotaPercentTolerance) &&
		withinIntTolerance(left.RemainingPercent, right.RemainingPercent, quotaPercentTolerance) &&
		int64PointersEqual(left.WindowDurationMins, right.WindowDurationMins) &&
		timestampsEquivalent(left.ResetsAt, right.ResetsAt)
}

func creditsEquivalent(left, right *model.Credits) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.HasCredits == right.HasCredits && left.Unlimited == right.Unlimited &&
		stringPointersEqual(left.Balance, right.Balance)
}

func individualLimitsEquivalent(left, right *model.IndividualLimit) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	// Used is a display total sampled independently from the rounded
	// percentage and can drift slightly between collectors. RemainingPercent
	// carries the bounded semantic comparison; Limit still defines the exact
	// bucket shape.
	return left.Limit == right.Limit &&
		withinIntTolerance(left.RemainingPercent, right.RemainingPercent, quotaPercentTolerance) &&
		withinInt64Tolerance(left.ResetsAt, right.ResetsAt, quotaResetTolerance)
}

func timestampsEquivalent(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return withinInt64Tolerance(*left, *right, quotaResetTolerance)
}

func normalizedDisplayText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func displayStringPointersEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return normalizedDisplayText(*left) == normalizedDisplayText(*right)
}

func stringPointersEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func boolPointersEqual(left, right *bool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func int64PointersEqual(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func withinIntTolerance(left, right, tolerance int) bool {
	difference := left - right
	if difference < 0 {
		difference = -difference
	}
	return difference <= tolerance
}

func withinInt64Tolerance(left, right, tolerance int64) bool {
	difference := left - right
	if difference < 0 {
		difference = -difference
	}
	return difference <= tolerance
}

func userStatus(stored entry, now time.Time, staleAfter time.Duration) model.UserStatus {
	version := stored.codexVersion
	versionAt := stored.codexVersionAt
	var versionObservedAt *time.Time
	if version != "" && !versionAt.IsZero() {
		value := versionAt.UTC()
		versionObservedAt = &value
	}
	return model.UserStatus{
		Username:               stored.identity.Username,
		CodexVersion:           version,
		CodexVersionObservedAt: versionObservedAt,
		ActiveChatsKnown: roleFor(stored.identity) == model.UserRoleConsumer &&
			stored.current.State == model.StateOK && !entryStale(stored, now, staleAfter) &&
			runtimeInventoryRecent(stored, now) && !stored.runtimeQuarantinePending &&
			len(stored.runtimeQuarantine) == 0,
		State:      stored.current.State,
		Role:       roleFor(stored.identity),
		LastSeenAt: stored.lastSeenAt,
		LastGoodAt: cloneTime(stored.lastGoodAt),
		Stale:      entryStale(stored, now, staleAfter),
	}
}

func runtimeInventoryRecent(stored entry, now time.Time) bool {
	if stored.runtimeInventoryAt.IsZero() {
		return false
	}
	return now.Before(stored.runtimeInventoryAt) ||
		now.Sub(stored.runtimeInventoryAt) <= runtimeInventoryGrace
}

func roleFor(identity Identity) model.UserRole {
	if identity.ExpectedEmail != "" {
		return model.UserRoleAnchor
	}
	return model.UserRoleConsumer
}

func entryStale(stored entry, now time.Time, staleAfter time.Duration) bool {
	return stored.lastSeenAt.IsZero() || stored.current.State == model.StateUnavailable ||
		now.Sub(stored.lastSeenAt) > staleAfter
}

func anchorHealth(stored entry, expectedKey string, now time.Time, staleAfter time.Duration) model.AnchorHealth {
	switch stored.current.State {
	case model.StateSignedOut, model.StateAPIKey:
		return model.AnchorHealthSignedOut
	case model.StateUnavailable:
		return model.AnchorHealthStale
	case model.StateOK:
		if entryStale(stored, now, staleAfter) {
			return model.AnchorHealthStale
		}
		if snapshotAccountKey(stored.current) != expectedKey {
			return model.AnchorHealthWrongAccount
		}
		return model.AnchorHealthOK
	default:
		return model.AnchorHealthStale
	}
}

func snapshotAccountKey(snapshot model.Snapshot) string {
	if snapshot.Account == nil || snapshot.Account.Type != "chatgpt" || snapshot.Account.Email == nil ||
		strings.TrimSpace(*snapshot.Account.Email) == "" {
		return ""
	}
	return model.AccountKey(*snapshot.Account.Email)
}

func snapshotAccountKeyFromPointer(snapshot *model.Snapshot) string {
	if snapshot == nil {
		return ""
	}
	return snapshotAccountKey(*snapshot)
}

func accountDisplayEmail(status model.AccountStatus) string {
	if status.Account == nil || status.Account.Email == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(*status.Account.Email))
}

func (h *Hub) Subscribe() (<-chan model.StatusResponse, func()) {
	h.mu.Lock()
	id := h.nextSubID
	h.nextSubID++
	ch := make(chan model.StatusResponse, 1)
	h.subs[id] = ch
	ch <- h.statusLocked(h.now().UTC())
	h.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			if existing, ok := h.subs[id]; ok {
				delete(h.subs, id)
				close(existing)
			}
			h.mu.Unlock()
		})
	}
	return ch, cancel
}

func (h *Hub) broadcastLocked(response model.StatusResponse) {
	for _, ch := range h.subs {
		select {
		case ch <- response:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- response:
			default:
			}
		}
	}
}

func (h *Hub) SeedDemo() {
	base := h.now().UTC()
	h.SetDemo(true)
	for index, username := range h.order {
		stored := h.entries[username]
		email := stored.identity.ExpectedEmail
		if email == "" {
			email = "localunitarity+demo-" + username + "@gmail.com"
		}
		plan := "plus"
		name := "Codex"
		shortWindow := int64(300)
		longWindow := int64(10_080)
		resetPrimary := base.Add(time.Duration(65+index*23) * time.Minute).Unix()
		resetSecondary := base.Add(time.Duration(3+index) * 24 * time.Hour).Unix()
		resetCredits := int64(index % 3)
		lifetime := int64(1_000_000 + index*125_000)
		snapshot := model.Snapshot{
			SchemaVersion:         model.SchemaVersion,
			Username:              username,
			State:                 model.StateOK,
			RuntimeThreadsRead:    true,
			Account:               &model.Account{Type: "chatgpt", Email: &email, PlanType: plan},
			ObservedAt:            base,
			LifetimeTokens:        &lifetime,
			LifetimeTokensRead:    true,
			ResetCreditsAvailable: &resetCredits,
			MainUsage: &model.Window{
				UsedPercent: index * 7, WindowDurationMins: &longWindow, ResetsAt: &resetSecondary,
			},
			Limits: []model.RateLimit{{
				ID: "codex", Name: &name, PlanType: &plan,
				Primary:   &model.Window{UsedPercent: index * 11, WindowDurationMins: &shortWindow, ResetsAt: &resetPrimary},
				Secondary: &model.Window{UsedPercent: index * 7, WindowDurationMins: &longWindow, ResetsAt: &resetSecondary},
			}},
		}
		snapshot.Normalize()
		for uid, candidate := range h.byUID {
			if candidate == username {
				_ = h.Apply(uid, snapshot)
				break
			}
		}
	}
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneLimits(value []model.RateLimit) []model.RateLimit {
	if value == nil {
		return []model.RateLimit{}
	}
	copy := make([]model.RateLimit, len(value))
	for i := range value {
		copy[i] = value[i]
		if value[i].Name != nil {
			v := *value[i].Name
			copy[i].Name = &v
		}
		if value[i].PlanType != nil {
			v := *value[i].PlanType
			copy[i].PlanType = &v
		}
		if value[i].ReachedType != nil {
			v := *value[i].ReachedType
			copy[i].ReachedType = &v
		}
		copy[i].Primary = cloneWindow(value[i].Primary)
		copy[i].Secondary = cloneWindow(value[i].Secondary)
		if value[i].Credits != nil {
			v := *value[i].Credits
			if value[i].Credits.Balance != nil {
				balance := *value[i].Credits.Balance
				v.Balance = &balance
			}
			copy[i].Credits = &v
		}
		if value[i].IndividualLimit != nil {
			v := *value[i].IndividualLimit
			copy[i].IndividualLimit = &v
		}
		if value[i].SpendControlReached != nil {
			v := *value[i].SpendControlReached
			copy[i].SpendControlReached = &v
		}
	}
	return copy
}

func cloneWindow(value *model.Window) *model.Window {
	if value == nil {
		return nil
	}
	copy := *value
	if value.WindowDurationMins != nil {
		duration := *value.WindowDurationMins
		copy.WindowDurationMins = &duration
	}
	if value.ResetsAt != nil {
		reset := *value.ResetsAt
		copy.ResetsAt = &reset
	}
	return &copy
}

func cloneAccount(value *model.Account) *model.Account {
	if value == nil {
		return nil
	}
	copy := *value
	if value.Email != nil {
		email := *value.Email
		copy.Email = &email
	}
	return &copy
}

func cloneSnapshot(value model.Snapshot) model.Snapshot {
	value.Account = cloneAccount(value.Account)
	value.MainUsage = cloneWindow(value.MainUsage)
	value.ResetCreditsAvailable = cloneInt64(value.ResetCreditsAvailable)
	value.LifetimeTokens = cloneInt64(value.LifetimeTokens)
	value.CodexVersionObservedAt = cloneTime(value.CodexVersionObservedAt)
	value.Limits = cloneLimits(value.Limits)
	if value.RecentThreads != nil {
		value.RecentThreads = append([]model.RecentThread(nil), value.RecentThreads...)
	}
	if value.RuntimeThreads != nil {
		value.RuntimeThreads = append([]model.RuntimeThread(nil), value.RuntimeThreads...)
	}
	return value
}
