package hub

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	usagehistory "codex-usage-dashboard/internal/history"
	"codex-usage-dashboard/internal/model"
)

type Identity struct {
	Username string
	UID      uint32
}

type entry struct {
	snapshot   model.Snapshot
	lastSeenAt time.Time
	lastGoodAt *time.Time
}

type Hub struct {
	applyMu    sync.Mutex
	mu         sync.RWMutex
	order      []string
	byUID      map[uint32]string
	entries    map[string]entry
	staleAfter time.Duration
	now        func() time.Time
	demo       bool
	revision   uint64
	nextSubID  uint64
	subs       map[uint64]chan model.StatusResponse
	history    *usagehistory.Tracker
}

func (h *Hub) SetHistory(tracker *usagehistory.Tracker) {
	h.applyMu.Lock()
	defer h.applyMu.Unlock()
	h.mu.Lock()
	h.history = tracker
	h.mu.Unlock()
}

func New(identities []Identity, staleAfter time.Duration) (*Hub, error) {
	if staleAfter <= 0 {
		return nil, errors.New("stale-after must be positive")
	}
	h := &Hub{
		byUID:      make(map[uint32]string, len(identities)),
		entries:    make(map[string]entry, len(identities)),
		staleAfter: staleAfter,
		now:        time.Now,
		subs:       make(map[uint64]chan model.StatusResponse),
	}
	seenNames := make(map[string]bool, len(identities))
	for _, identity := range identities {
		if identity.Username == "" {
			return nil, errors.New("identity username is required")
		}
		if seenNames[identity.Username] {
			return nil, fmt.Errorf("duplicate username %q", identity.Username)
		}
		if prior, ok := h.byUID[identity.UID]; ok {
			return nil, fmt.Errorf("UID %d belongs to both %q and %q", identity.UID, prior, identity.Username)
		}
		seenNames[identity.Username] = true
		h.byUID[identity.UID] = identity.Username
		h.order = append(h.order, identity.Username)
		h.entries[identity.Username] = entry{snapshot: model.Snapshot{
			SchemaVersion: model.SchemaVersion,
			Username:      identity.Username,
			State:         model.StateUnavailable,
			Limits:        []model.RateLimit{},
			ErrorCategory: model.ErrorAwaitingCollector,
		}}
	}
	return h, nil
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
	tracker := h.history
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
	if tracker != nil {
		// Persistence may fsync. Keep it outside h.mu so status and SSE readers
		// remain available; applyMu preserves collector update order.
		tracker.Observe(incoming)
	}

	h.mu.Lock()
	now := h.now().UTC()
	current := h.entries[username]
	if incoming.State == model.StateUnavailable && current.lastGoodAt != nil {
		// Preserve useful account/limit data while making the current failure explicit.
		incoming.Account = cloneAccount(current.snapshot.Account)
		incoming.MainUsage = cloneWindow(current.snapshot.MainUsage)
		incoming.Limits = cloneLimits(current.snapshot.Limits)
		incoming.ObservedAt = current.snapshot.ObservedAt
	} else {
		goodAt := now
		current.lastGoodAt = &goodAt
	}
	current.snapshot = incoming
	current.lastSeenAt = now
	h.entries[username] = current
	h.revision++
	response := h.statusLocked(now)
	h.broadcastLocked(response)
	h.mu.Unlock()
	return nil
}

func (h *Hub) Status() model.StatusResponse {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.statusLocked(h.now().UTC())
}

func (h *Hub) statusLocked(now time.Time) model.StatusResponse {
	accounts := make([]model.AccountStatus, 0, len(h.order))
	for _, username := range h.order {
		stored := h.entries[username]
		snapshot := cloneSnapshot(stored.snapshot)
		stale := stored.lastGoodAt == nil || now.Sub(*stored.lastGoodAt) > h.staleAfter
		status := model.AccountStatus{
			Snapshot:   snapshot,
			LastSeenAt: stored.lastSeenAt,
			LastGoodAt: cloneTime(stored.lastGoodAt),
			Stale:      stale,
		}
		accounts = append(accounts, status)
	}
	return model.StatusResponse{
		SchemaVersion: model.SchemaVersion,
		Revision:      h.revision,
		GeneratedAt:   now,
		Demo:          h.demo,
		Accounts:      accounts,
	}
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
	identities := make([]struct {
		uid   uint32
		email string
		plan  string
		used  int
	}, 0, len(h.byUID))
	for uid, username := range h.byUID {
		identities = append(identities, struct {
			uid   uint32
			email string
			plan  string
			used  int
		}{uid: uid, email: username + "@example.com", plan: "plus", used: 18 + len(identities)*17})
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i].uid < identities[j].uid })
	shortWindow := int64(300)
	longWindow := int64(10080)
	for i, identity := range identities {
		email := identity.email
		name := "Codex"
		plan := identity.plan
		resetPrimary := base.Add(time.Duration(65+i*23) * time.Minute).Unix()
		resetSecondary := base.Add(time.Duration(3+i) * 24 * time.Hour).Unix()
		snapshot := model.Snapshot{
			SchemaVersion: model.SchemaVersion,
			Username:      h.byUID[identity.uid],
			State:         model.StateOK,
			Account:       &model.Account{Type: "chatgpt", Email: &email, PlanType: plan},
			ObservedAt:    base,
			MainUsage: &model.Window{
				UsedPercent: identity.used / 2, WindowDurationMins: &longWindow, ResetsAt: &resetSecondary,
			},
			Limits: []model.RateLimit{{
				ID:       "codex",
				Name:     &name,
				PlanType: &plan,
				Primary: &model.Window{
					UsedPercent: identity.used, WindowDurationMins: &shortWindow, ResetsAt: &resetPrimary,
				},
				Secondary: &model.Window{
					UsedPercent: identity.used / 2, WindowDurationMins: &longWindow, ResetsAt: &resetSecondary,
				},
			}},
		}
		snapshot.Normalize()
		_ = h.Apply(identity.uid, snapshot)
	}
}

func cloneTime(value *time.Time) *time.Time {
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
		if value[i].Primary != nil {
			v := *value[i].Primary
			if value[i].Primary.WindowDurationMins != nil {
				duration := *value[i].Primary.WindowDurationMins
				v.WindowDurationMins = &duration
			}
			if value[i].Primary.ResetsAt != nil {
				reset := *value[i].Primary.ResetsAt
				v.ResetsAt = &reset
			}
			copy[i].Primary = &v
		}
		if value[i].Secondary != nil {
			v := *value[i].Secondary
			if value[i].Secondary.WindowDurationMins != nil {
				duration := *value[i].Secondary.WindowDurationMins
				v.WindowDurationMins = &duration
			}
			if value[i].Secondary.ResetsAt != nil {
				reset := *value[i].Secondary.ResetsAt
				v.ResetsAt = &reset
			}
			copy[i].Secondary = &v
		}
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
	value.Limits = cloneLimits(value.Limits)
	return value
}
