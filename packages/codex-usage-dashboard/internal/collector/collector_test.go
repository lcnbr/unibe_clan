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
	accountErr    error
	limitsErr     error
	accountReads  int
	limitReads    int
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
	return c
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

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("initial snapshot was not published")
	}
	fake.notifications <- "account/updated"
	select {
	case <-published:
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
		t.Fatal("initial snapshot was not published")
	}
	if err := os.WriteFile(authPath, []byte("test-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for starts.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if starts.Load() < 2 {
		t.Fatalf("collector starts = %d, want at least 2", starts.Load())
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
