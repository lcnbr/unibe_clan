package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex-usage-dashboard/internal/codex"
	"codex-usage-dashboard/internal/model"
)

type fakeAppServer struct {
	mu            sync.Mutex
	account       codex.AccountResponse
	limits        codex.RateLimitsResponse
	usage         codex.AccountUsageResponse
	threads       codex.ThreadListResponse
	accountErr    error
	limitsErr     error
	usageErr      error
	threadsErr    error
	accountReads  int
	limitReads    int
	usageReads    int
	threadReads   int
	notifications chan string
	done          chan struct{}
	err           error
	closeOnce     sync.Once
}

func newFakeAppServer() *fakeAppServer {
	email := "person@example.com"
	plan := "pro"
	limitID := "codex"
	return &fakeAppServer{
		account: codex.AccountResponse{Account: &codex.Account{Type: "chatgpt", Email: &email, PlanType: &plan}},
		limits: codex.RateLimitsResponse{RateLimitsByLimitID: map[string]codex.RateLimitSnapshot{
			"codex": {LimitID: &limitID, Primary: &codex.RateLimitWindow{UsedPercent: 25}},
		}},
		notifications: make(chan string, 4),
		done:          make(chan struct{}),
	}
}

func (f *fakeAppServer) Account(context.Context) (codex.AccountResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accountReads++
	return f.account, f.accountErr
}

func (f *fakeAppServer) RateLimits(context.Context) (codex.RateLimitsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limitReads++
	return f.limits, f.limitsErr
}

func (f *fakeAppServer) AccountUsage(context.Context) (codex.AccountUsageResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usageReads++
	return f.usage, f.usageErr
}

func (f *fakeAppServer) Threads(context.Context, int) (codex.ThreadListResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threadReads++
	return f.threads, f.threadsErr
}

func (f *fakeAppServer) Notifications() <-chan string { return f.notifications }
func (f *fakeAppServer) Done() <-chan struct{}        { return f.done }
func (f *fakeAppServer) Err() error                   { return f.err }
func (f *fakeAppServer) Close() error {
	f.closeOnce.Do(func() { close(f.done) })
	return nil
}

func (f *fakeAppServer) reads() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accountReads, f.limitReads
}

func testCollector(t *testing.T) *Collector {
	t.Helper()
	c, err := New(Config{
		Username:        "codex-2",
		SocketPath:      filepath.Join(t.TempDir(), "ingest.sock"),
		AuthPath:        filepath.Join(t.TempDir(), "auth.json"),
		PollInterval:    time.Hour,
		RecycleInterval: time.Hour,
		StatInterval:    time.Hour,
		RequestTimeout:  time.Second,
		PublishTimeout:  time.Second,
		BackoffMin:      time.Millisecond,
		BackoffMax:      2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	c.stat = func(string) (authMetadata, error) {
		return authMetadata{Known: true, Exists: true}, nil
	}
	c.liveVersion = func(context.Context) string { return "" }
	return c
}

func TestCollectorClonesAndValidatesCodexVersionTargets(t *testing.T) {
	target := "/nix/store/lp8pgfpak48rdgxn3pqgjq51i05kjj7i-codex-0.151.0/bin/.codex-wrapped"
	configured := map[string]string{target: "0.151.0"}
	c, err := New(Config{
		Username: "codex", SocketPath: "/run/test.sock", AuthPath: "/home/codex/.codex/auth.json",
		CodexVersionTargets: configured,
	})
	if err != nil {
		t.Fatal(err)
	}
	configured[target] = "9.9.9"
	if got := c.cfg.CodexVersionTargets[target]; got != "0.151.0" {
		t.Fatalf("collector retained mutable caller map: %q", got)
	}

	for _, invalid := range []map[string]string{
		{"relative/codex": "0.151.0"},
		{"/nix/store/../codex": "0.151.0"},
		{target: "0.151.0\nprivate"},
	} {
		if _, err := New(Config{
			Username: "codex", SocketPath: "/run/test.sock", AuthPath: "/home/codex/.codex/auth.json",
			CodexVersionTargets: invalid,
		}); err == nil {
			t.Fatalf("invalid version targets accepted: %#v", invalid)
		}
	}
}

func TestCollectorDoesNotClaimPinnedCLIAsUserVersionWithoutLiveProcess(t *testing.T) {
	c := testCollector(t)
	fake := newFakeAppServer()
	published := make([]model.Snapshot, 0, 3)
	c.publish = func(_ context.Context, snapshot model.Snapshot) error {
		published = append(published, snapshot)
		return nil
	}
	if ok, category := c.refresh(context.Background(), fake); !ok || category != "" {
		t.Fatalf("healthy refresh = (%v, %q)", ok, category)
	}
	c.publishUnavailable(context.Background(), model.ErrorCodexUnavailable)
	c.publishSignedOut(context.Background())
	if len(published) != 3 {
		t.Fatalf("published %d snapshots, want 3", len(published))
	}
	for _, snapshot := range published {
		if snapshot.CodexVersion != "" || snapshot.CodexVersionObservedAt != nil {
			t.Fatalf("%s snapshot claimed version evidence without a live process: %#v", snapshot.State, snapshot)
		}
		if err := snapshot.Validate(); err != nil {
			t.Fatalf("%s snapshot validation: %v", snapshot.State, err)
		}
	}
}

func TestLiveCodexVersionCarriesItsExactObservationTime(t *testing.T) {
	c := testCollector(t)
	now := time.Date(2026, time.September, 7, 10, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	c.liveVersion = func(context.Context) string { return "0.153.4" }
	fake := newFakeAppServer()
	var published model.Snapshot
	c.publish = func(_ context.Context, snapshot model.Snapshot) error {
		published = snapshot
		return nil
	}

	if ok, category := c.refresh(context.Background(), fake); !ok || category != "" {
		t.Fatalf("refresh = (%v, %q)", ok, category)
	}
	if published.CodexVersion != "0.153.4" || published.CodexVersionObservedAt == nil ||
		!published.CodexVersionObservedAt.Equal(now) {
		t.Fatalf("live version observation = %#v", published)
	}
}

func TestNextBackoffIsExponentialAndBounded(t *testing.T) {
	maximum := 30 * time.Second
	values := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, maximum, maximum}
	current := values[0]
	for index := 1; index < len(values); index++ {
		current = nextBackoff(current, maximum)
		if current != values[index] {
			t.Fatalf("step %d = %v, want %v", index, current, values[index])
		}
	}
}

func TestRunRefetchesFullStateOnNotification(t *testing.T) {
	c := testCollector(t)
	fake := newFakeAppServer()
	c.start = func(context.Context) (appServer, error) { return fake, nil }
	published := make(chan model.Snapshot, 4)
	c.publish = func(_ context.Context, snapshot model.Snapshot) error {
		published <- snapshot
		return nil
	}
	initialCount := int64(1)
	fake.mu.Lock()
	fake.limits.ResetCredits = &codex.ResetCreditsSummary{AvailableCount: &initialCount}
	fake.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case initial := <-published:
		if initial.ResetCreditsAvailable == nil || *initial.ResetCreditsAvailable != 1 {
			t.Fatalf("initial reset-credit count = %#v", initial.ResetCreditsAvailable)
		}
	case <-time.After(time.Second):
		t.Fatal("initial snapshot was not published")
	}
	updatedCount := int64(3)
	fake.mu.Lock()
	fake.limits.ResetCredits = &codex.ResetCreditsSummary{AvailableCount: &updatedCount}
	fake.mu.Unlock()
	fake.notifications <- "account/updated"
	select {
	case updated := <-published:
		if updated.ResetCreditsAvailable == nil || *updated.ResetCreditsAvailable != 3 {
			t.Fatalf("updated reset-credit count = %#v", updated.ResetCreditsAvailable)
		}
	case <-time.After(time.Second):
		t.Fatal("notification did not trigger a refetch")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	accountReads, limitReads := fake.reads()
	if accountReads < 2 || limitReads < 2 {
		t.Fatalf("reads = account:%d limits:%d, want at least two of each", accountReads, limitReads)
	}
}

func TestRefreshMissingEmailPublishesUnavailableWithoutReadingLimits(t *testing.T) {
	c := testCollector(t)
	fake := newFakeAppServer()
	fake.account.Account.Email = nil
	var published model.Snapshot
	c.publish = func(_ context.Context, snapshot model.Snapshot) error {
		published = snapshot
		return nil
	}
	ok, category := c.refresh(context.Background(), fake)
	if !ok || category != "" {
		t.Fatalf("refresh = (%v, %q), want successful unavailable snapshot", ok, category)
	}
	if published.State != model.StateUnavailable || published.ErrorCategory != model.ErrorAuthUnavailable || published.Account != nil || len(published.Limits) != 0 {
		t.Fatalf("unexpected strict snapshot: %#v", published)
	}
	_, limitReads := fake.reads()
	if limitReads != 0 {
		t.Fatalf("rate limits read %d times for account without display identity", limitReads)
	}
}

func TestRefreshMissingPlanIsProtocolFailure(t *testing.T) {
	c := testCollector(t)
	fake := newFakeAppServer()
	fake.account.Account.PlanType = nil
	published := false
	c.publish = func(_ context.Context, snapshot model.Snapshot) error {
		published = true
		return nil
	}
	ok, category := c.refresh(context.Background(), fake)
	if ok || category != model.ErrorProtocol {
		t.Fatalf("refresh = (%v, %q), want protocol failure", ok, category)
	}
	if published {
		t.Fatal("incompatible account payload was published as healthy")
	}
	_, limitReads := fake.reads()
	if limitReads != 0 {
		t.Fatalf("rate limits read %d times after incompatible account payload", limitReads)
	}
}

func TestOptionalUsageAndThreadFailuresDoNotBlankQuota(t *testing.T) {
	c := testCollector(t)
	c.runtimeThreads = func(context.Context, string) (codex.ThreadListResponse, error) {
		return codex.ThreadListResponse{}, errors.New("optional control daemon unavailable")
	}
	fake := newFakeAppServer()
	fake.usageErr = errors.New("optional usage unavailable")
	fake.threadsErr = errors.New("optional threads unavailable")
	var published model.Snapshot
	c.publish = func(_ context.Context, snapshot model.Snapshot) error {
		published = snapshot
		return nil
	}
	ok, category := c.refresh(context.Background(), fake)
	if !ok || category != "" {
		t.Fatalf("refresh = (%v, %q)", ok, category)
	}
	if published.State != model.StateOK || len(published.Limits) != 1 ||
		published.LifetimeTokens != nil || published.LifetimeTokensRead ||
		published.RecentThreadsRead || len(published.RecentThreads) != 0 ||
		published.RuntimeThreadsRead || len(published.RuntimeThreads) != 0 {
		t.Fatalf("optional failure changed quota snapshot: %#v", published)
	}
}

func TestControlAccountMatchIsTrimmedCaseInsensitiveAndChatGPTOnly(t *testing.T) {
	email := "  Person+Alias@GMAIL.COM  "
	if !sameChatGPTAccount(codex.AccountResponse{Account: &codex.Account{
		Type: "chatgpt", Email: &email,
	}}, "person+alias@gmail.com") {
		t.Fatal("equivalent ChatGPT emails did not match")
	}
	other := "other@gmail.com"
	for _, response := range []codex.AccountResponse{
		{},
		{Account: &codex.Account{Type: "chatgpt"}},
		{Account: &codex.Account{Type: "apiKey", Email: &email}},
		{Account: &codex.Account{Type: "chatgpt", Email: &other}},
	} {
		if sameChatGPTAccount(response, "person+alias@gmail.com") {
			t.Fatalf("mismatched control account accepted: %#v", response.Account)
		}
	}
}

func TestAccountSwitchClearsOptionalRuntimeWhenControlIdentityLags(t *testing.T) {
	c := testCollector(t)
	fake := newFakeAppServer()
	controlEmail := "person@example.com"
	c.runtimeThreads = func(_ context.Context, expectedEmail string) (codex.ThreadListResponse, error) {
		controlAccount := codex.AccountResponse{Account: &codex.Account{
			Type: "chatgpt", Email: &controlEmail,
		}}
		if !sameChatGPTAccount(controlAccount, expectedEmail) {
			return codex.ThreadListResponse{}, codex.ErrProtocol
		}
		return codex.ThreadListResponse{Threads: []codex.Thread{{
			ID: "private-runtime-id", SessionID: "runtime-session", Source: codex.SessionSourceCLI,
			Status: codex.ThreadStatus{Type: "active"}, CreatedAt: 10, UpdatedAt: 20,
		}}}, nil
	}
	var published model.Snapshot
	c.publish = func(_ context.Context, snapshot model.Snapshot) error {
		published = snapshot
		return nil
	}
	if ok, category := c.refresh(context.Background(), fake); !ok || category != "" {
		t.Fatalf("initial refresh = (%v, %q)", ok, category)
	}
	if !published.RuntimeThreadsRead || len(published.RuntimeThreads) != 1 {
		t.Fatalf("matched runtime observation = read:%v threads:%#v", published.RuntimeThreadsRead, published.RuntimeThreads)
	}

	switchedEmail := "second@example.com"
	fake.mu.Lock()
	fake.account.Account.Email = &switchedEmail
	fake.mu.Unlock()
	if ok, category := c.refresh(context.Background(), fake); !ok || category != "" {
		t.Fatalf("switched refresh = (%v, %q)", ok, category)
	}
	if published.RuntimeThreadsRead || len(published.RuntimeThreads) != 0 {
		t.Fatalf("mismatched control identity retained runtime: read:%v threads:%#v",
			published.RuntimeThreadsRead, published.RuntimeThreads)
	}
}

func TestRefreshPublishesTopLevelRuntimeStatusAndClearsItOnFailure(t *testing.T) {
	c := testCollector(t)
	fake := newFakeAppServer()
	activeName := " Active\n dashboard "
	idleName := "Idle dashboard"
	parent := "parent-thread"
	c.runtimeThreads = func(context.Context, string) (codex.ThreadListResponse, error) {
		return codex.ThreadListResponse{Threads: []codex.Thread{
			{ID: "private-active", SessionID: "active", Source: codex.SessionSourceCLI, Name: &activeName, Status: codex.ThreadStatus{Type: "active"}, CreatedAt: 10, UpdatedAt: 20},
			{ID: "private-idle", SessionID: "idle", Source: codex.SessionSourceVSCode, Name: &idleName, Status: codex.ThreadStatus{Type: "idle"}, CreatedAt: 30, UpdatedAt: 40},
			{ID: "private-old", SessionID: "old", Source: codex.SessionSourceExec, Status: codex.ThreadStatus{Type: "notLoaded"}},
			{ID: "private-subagent", SessionID: "active", Source: codex.SessionSourceCLI, ParentThreadID: &parent, Status: codex.ThreadStatus{Type: "active"}},
		}}, nil
	}
	var published model.Snapshot
	c.publish = func(_ context.Context, snapshot model.Snapshot) error {
		published = snapshot
		return nil
	}
	if ok, category := c.refresh(context.Background(), fake); !ok || category != "" {
		t.Fatalf("refresh = (%v, %q)", ok, category)
	}
	if !published.RuntimeThreadsRead || len(published.RuntimeThreads) != 2 ||
		published.RuntimeThreads[0].TaskName != "Active dashboard" ||
		!published.RuntimeThreads[0].Running || published.RuntimeThreads[1].Running {
		t.Fatalf("runtime observation = %#v", published.RuntimeThreads)
	}

	// A transient read failure produces an explicitly unavailable, empty
	// observation. The Hub replaces its previous set on every OK snapshot so
	// this cannot leave a false running chat behind.
	c.runtimeThreads = func(context.Context, string) (codex.ThreadListResponse, error) {
		return codex.ThreadListResponse{}, codex.ErrClosed
	}
	if ok, category := c.refresh(context.Background(), fake); !ok || category != "" {
		t.Fatalf("refresh after control failure = (%v, %q)", ok, category)
	}
	if published.RuntimeThreadsRead || len(published.RuntimeThreads) != 0 {
		t.Fatalf("failed runtime observation retained activity: %#v", published.RuntimeThreads)
	}
}

func TestMissingControlSocketIsConfirmedEmptyRuntimeObservation(t *testing.T) {
	c := testCollector(t)
	fake := newFakeAppServer()
	var published model.Snapshot
	c.publish = func(_ context.Context, snapshot model.Snapshot) error {
		published = snapshot
		return nil
	}
	if ok, category := c.refresh(context.Background(), fake); !ok || category != "" {
		t.Fatalf("refresh = (%v, %q)", ok, category)
	}
	if !published.RuntimeThreadsRead || len(published.RuntimeThreads) != 0 {
		t.Fatalf("missing control socket = read:%v threads:%#v", published.RuntimeThreadsRead, published.RuntimeThreads)
	}
}

func TestRefreshPublishesAllowlistedLifetimeAndThreadMetadata(t *testing.T) {
	c := testCollector(t)
	fake := newFakeAppServer()
	lifetime := int64(987654)
	taskName := "  Fix\n dashboard\t now  "
	fake.usage = codex.AccountUsageResponse{LifetimeTokens: &lifetime}
	fake.threads = codex.ThreadListResponse{Threads: []codex.Thread{{
		ID: "private-thread-id", SessionID: "session-safe-id", Source: codex.SessionSourceAppServer,
		Name: &taskName, CreatedAt: 10, UpdatedAt: 20,
	}}}
	var published model.Snapshot
	c.publish = func(_ context.Context, snapshot model.Snapshot) error {
		published = snapshot
		return nil
	}
	if ok, category := c.refresh(context.Background(), fake); !ok || category != "" {
		t.Fatalf("refresh = (%v, %q)", ok, category)
	}
	if !published.LifetimeTokensRead || published.LifetimeTokens == nil ||
		*published.LifetimeTokens != lifetime || !published.RecentThreadsRead ||
		len(published.RecentThreads) != 1 || published.RecentThreads[0].ThreadID != "session-safe-id" ||
		published.RecentThreads[0].TaskName != "Fix dashboard now" || published.CodexVersion != "" {
		t.Fatalf("optional allowlist snapshot: %#v", published)
	}
}

func TestAuthMetadataChangeRecyclesChild(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "auth.json")
	c, err := New(Config{
		Username: "codex-2", SocketPath: filepath.Join(t.TempDir(), "ingest.sock"), AuthPath: authPath,
		PollInterval: time.Hour, RecycleInterval: time.Hour, StatInterval: 5 * time.Millisecond,
		RequestTimeout: time.Second, PublishTimeout: time.Second,
		BackoffMin: time.Millisecond, BackoffMax: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	var starts atomic.Int32
	c.start = func(context.Context) (appServer, error) {
		starts.Add(1)
		return newFakeAppServer(), nil
	}
	firstPublish := make(chan struct{}, 1)
	c.publish = func(_ context.Context, _ model.Snapshot) error {
		select {
		case firstPublish <- struct{}{}:
		default:
		}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case <-firstPublish:
	case <-time.After(time.Second):
		t.Fatal("initial signed-out snapshot was not published")
	}
	if starts.Load() != 0 {
		t.Fatalf("collector started %d app servers before auth existed", starts.Load())
	}
	if err := os.WriteFile(authPath, []byte("test-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for starts.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if starts.Load() < 1 {
		t.Fatalf("collector starts = %d, want at least 1 after auth appeared", starts.Load())
	}
}

func TestSignedOutPublishFailureIsRetriedWithoutStartingAppServer(t *testing.T) {
	c := testCollector(t)
	c.cfg.StatInterval = 5 * time.Millisecond
	c.stat = func(string) (authMetadata, error) {
		return authMetadata{Known: true, Exists: false}, nil
	}
	var starts atomic.Int32
	c.start = func(context.Context) (appServer, error) {
		starts.Add(1)
		return newFakeAppServer(), nil
	}
	var attempts atomic.Int32
	published := make(chan model.Snapshot, 1)
	c.publish = func(_ context.Context, snapshot model.Snapshot) error {
		if attempts.Add(1) == 1 {
			return errors.New("dashboard socket is not ready")
		}
		published <- snapshot
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case snapshot := <-published:
		if snapshot.State != model.StateSignedOut {
			t.Fatalf("retried snapshot state = %q, want %q", snapshot.State, model.StateSignedOut)
		}
	case <-time.After(time.Second):
		t.Fatal("signed-out snapshot was not retried after the initial publish failure")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if attempts.Load() < 2 {
		t.Fatalf("publish attempts = %d, want at least 2", attempts.Load())
	}
	if starts.Load() != 0 {
		t.Fatalf("collector started %d app servers without auth", starts.Load())
	}
}

func TestRunPublishesSafeFailureCategory(t *testing.T) {
	c := testCollector(t)
	secret := errors.New("sk-secret person@example.com")
	c.start = func(context.Context) (appServer, error) { return nil, secret }
	published := make(chan model.Snapshot, 1)
	c.publish = func(_ context.Context, snapshot model.Snapshot) error {
		published <- snapshot
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	var snapshot model.Snapshot
	select {
	case snapshot = <-published:
	case <-time.After(time.Second):
		t.Fatal("failure snapshot was not published")
	}
	cancel()
	<-done
	payload, _ := json.Marshal(snapshot)
	if snapshot.ErrorCategory != model.ErrorCodexUnavailable || bytes.Contains(payload, []byte("secret")) || bytes.Contains(payload, []byte("example.com")) {
		t.Fatalf("unsafe failure snapshot: %s", payload)
	}
}

func TestPublishUnixUsesBoundedSingleSnapshotAndAck(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "ingest.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan []byte, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer conn.Close()
		payload, _ := io.ReadAll(io.LimitReader(conn, 64<<10))
		received <- payload
		_, _ = conn.Write([]byte("{\"ok\":true}\n"))
	}()
	email := "person@example.com"
	snapshot := model.Snapshot{
		Username: "codex-2", State: model.StateOK,
		Account: &model.Account{Type: "chatgpt", Email: &email, PlanType: "pro"},
		Limits:  []model.RateLimit{}, ObservedAt: time.Now().UTC(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := publishUnix(ctx, socketPath, 64<<10, snapshot); err != nil {
		t.Fatalf("publishUnix: %v", err)
	}
	payload := <-received
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		t.Fatalf("payload is not newline terminated: %q", payload)
	}
	var decoded model.Snapshot
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if decoded.Username != "codex-2" || decoded.Account == nil || decoded.Account.Email == nil || *decoded.Account.Email != email {
		t.Fatalf("unexpected decoded snapshot: %#v", decoded)
	}
	if err := publishUnix(ctx, socketPath, 1, snapshot); !errors.Is(err, errPublish) {
		t.Fatalf("oversized publish error = %v", err)
	}
}
