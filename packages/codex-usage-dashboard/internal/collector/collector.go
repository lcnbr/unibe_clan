package collector

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codex-usage-dashboard/internal/codex"
	"codex-usage-dashboard/internal/model"
)

const (
	defaultPollInterval    = 30 * time.Second
	defaultRecycleInterval = 5 * time.Minute
	defaultStatInterval    = 5 * time.Second
	defaultRequestTimeout  = 10 * time.Second
	defaultPublishTimeout  = 5 * time.Second
	defaultBackoffMin      = time.Second
	defaultBackoffMax      = 30 * time.Second
	defaultMaxPayload      = 64 << 10
	defaultMaxRPCLine      = 1 << 20
	maxVersionOutputBytes  = 256
	maxVersionProbeTime    = 2 * time.Second
	maxVersionProbeCleanup = 250 * time.Millisecond
)

// Config controls one per-user collector. It contains paths and timings only;
// credentials are owned and read exclusively by the Codex subprocess.
type Config struct {
	Username string

	CodexPath         string
	SocketPath        string
	AuthPath          string
	ControlSocketPath string

	PollInterval    time.Duration
	RecycleInterval time.Duration
	StatInterval    time.Duration
	RequestTimeout  time.Duration
	PublishTimeout  time.Duration
	BackoffMin      time.Duration
	BackoffMax      time.Duration

	MaxPayload int
	MaxRPCLine int
	Logger     *log.Logger
}

type appServer interface {
	Account(context.Context) (codex.AccountResponse, error)
	RateLimits(context.Context) (codex.RateLimitsResponse, error)
	AccountUsage(context.Context) (codex.AccountUsageResponse, error)
	Threads(context.Context, int) (codex.ThreadListResponse, error)
	Notifications() <-chan string
	Done() <-chan struct{}
	Err() error
	Close() error
}

// Collector supervises one private Codex App Server and publishes allowlisted
// snapshots to the dashboard's local ingest socket.
type Collector struct {
	cfg Config

	start          func(context.Context) (appServer, error)
	runtimeThreads func(context.Context, string) (codex.ThreadListResponse, error)
	detectVersion  func(context.Context, string) (string, error)
	publish        func(context.Context, model.Snapshot) error
	stat           func(string) (authMetadata, error)
	now            func() time.Time
	codexVersion   string

	logMu sync.Mutex
}

// New validates and applies production defaults. Run remains active until its
// context is canceled, reconnecting with bounded exponential backoff.
func New(cfg Config) (*Collector, error) {
	if cfg.Username == "" {
		return nil, errors.New("collector username is required")
	}
	if cfg.SocketPath == "" {
		return nil, errors.New("collector socket path is required")
	}
	if cfg.CodexPath == "" {
		cfg.CodexPath = "codex"
	}
	if cfg.AuthPath == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return nil, errors.New("collector auth path is required")
		}
		cfg.AuthPath = filepath.Join(home, ".codex", "auth.json")
	}
	if cfg.ControlSocketPath == "" {
		cfg.ControlSocketPath = filepath.Join(filepath.Dir(cfg.AuthPath), "app-server-control", "app-server-control.sock")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.RecycleInterval <= 0 {
		cfg.RecycleInterval = defaultRecycleInterval
	}
	if cfg.StatInterval <= 0 {
		cfg.StatInterval = defaultStatInterval
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultRequestTimeout
	}
	if cfg.PublishTimeout <= 0 {
		cfg.PublishTimeout = defaultPublishTimeout
	}
	if cfg.BackoffMin <= 0 {
		cfg.BackoffMin = defaultBackoffMin
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = defaultBackoffMax
	}
	if cfg.BackoffMax < cfg.BackoffMin {
		return nil, errors.New("collector maximum backoff is less than minimum")
	}
	if cfg.MaxPayload <= 0 {
		cfg.MaxPayload = defaultMaxPayload
	}
	if cfg.MaxRPCLine <= 0 {
		cfg.MaxRPCLine = defaultMaxRPCLine
	}

	c := &Collector{
		cfg: cfg, stat: statAuthMetadata, now: time.Now,
		detectVersion: detectCodexVersion,
	}
	c.start = func(ctx context.Context) (appServer, error) {
		return codex.Start(ctx, codex.Config{
			Path:             cfg.CodexPath,
			MaxLineBytes:     cfg.MaxRPCLine,
			HandshakeTimeout: cfg.RequestTimeout,
		})
	}
	c.runtimeThreads = func(ctx context.Context, expectedEmail string) (codex.ThreadListResponse, error) {
		info, err := os.Lstat(cfg.ControlSocketPath)
		if errors.Is(err, os.ErrNotExist) {
			// No daemon means there cannot be a loaded or running chat. Treat
			// that as a confirmed empty observation, not an optional failure.
			return codex.ThreadListResponse{Threads: []codex.Thread{}}, nil
		}
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			return codex.ThreadListResponse{}, codex.ErrClosed
		}
		client, err := codex.ConnectControl(ctx, codex.ControlConfig{
			SocketPath:       cfg.ControlSocketPath,
			MaxLineBytes:     cfg.MaxRPCLine,
			HandshakeTimeout: cfg.RequestTimeout,
		})
		if err != nil {
			return codex.ThreadListResponse{}, err
		}
		defer client.Close()
		controlAccount, err := client.Account(ctx)
		if err != nil || !sameChatGPTAccount(controlAccount, expectedEmail) {
			return codex.ThreadListResponse{}, codex.ErrProtocol
		}
		return client.LoadedThreads(ctx, model.MaxRuntimeThreads)
	}
	c.publish = func(ctx context.Context, snapshot model.Snapshot) error {
		return publishUnix(ctx, cfg.SocketPath, cfg.MaxPayload, snapshot)
	}
	return c, nil
}

// Run supervises the app-server process. Expected cancellation is reported as
// success so systemd can stop the service cleanly.
func (c *Collector) Run(ctx context.Context) error {
	c.probeCodexVersion(ctx)
	backoff := c.cfg.BackoffMin
	for {
		if ctx.Err() != nil {
			return nil
		}
		if !c.waitForAuthentication(ctx) {
			return nil
		}
		client, err := c.start(ctx)
		if err != nil {
			c.publishUnavailable(ctx, model.ErrorCodexUnavailable)
			c.logCategory(model.ErrorCodexUnavailable)
			if !waitContext(ctx, backoff) {
				return nil
			}
			backoff = nextBackoff(backoff, c.cfg.BackoffMax)
			continue
		}

		result := c.runSession(ctx, client)
		_ = client.Close()
		if ctx.Err() != nil {
			return nil
		}
		if result.stable || result.immediate {
			backoff = c.cfg.BackoffMin
		}
		if result.immediate {
			continue
		}
		if result.category == "" {
			result.category = model.ErrorCodexUnavailable
		}
		c.publishUnavailable(ctx, result.category)
		c.logCategory(result.category)
		if !waitContext(ctx, backoff) {
			return nil
		}
		backoff = nextBackoff(backoff, c.cfg.BackoffMax)
	}
}

// waitForAuthentication keeps an all-user collector almost entirely idle
// until its private auth file exists. It also publishes a definitive signed
// out state once per idle period so a removed auth file moves consumers to
// Unassigned users without starting Codex merely to confirm the logout.
func (c *Collector) waitForAuthentication(ctx context.Context) bool {
	published := false
	for {
		metadata, err := c.stat(c.cfg.AuthPath)
		if err == nil && metadata.Known && metadata.Exists {
			return true
		}
		if err == nil && !metadata.Known {
			// Test and alternate stat implementations may not be able to report
			// existence. Preserve the historical behavior in that case.
			return true
		}
		if !published {
			if err == nil {
				published = c.publishSignedOut(ctx)
			} else {
				published = c.publishUnavailable(ctx, model.ErrorAuthUnavailable)
			}
		}
		if !waitContext(ctx, c.cfg.StatInterval) {
			return false
		}
	}
}

type sessionResult struct {
	stable    bool
	immediate bool
	category  string
}

func (c *Collector) runSession(ctx context.Context, client appServer) sessionResult {
	baseline, _ := c.stat(c.cfg.AuthPath)
	_, category := c.refresh(ctx, client)
	if category != "" {
		return sessionResult{category: category}
	}
	stable := false

	poll := time.NewTicker(c.cfg.PollInterval)
	defer poll.Stop()
	stat := time.NewTicker(c.cfg.StatInterval)
	defer stat.Stop()
	recycle := time.NewTimer(c.cfg.RecycleInterval)
	defer recycle.Stop()

	notifications := client.Notifications()
	for {
		select {
		case <-ctx.Done():
			return sessionResult{stable: stable, immediate: true}
		case <-client.Done():
			return sessionResult{stable: stable, category: clientErrorCategory(client.Err())}
		case _, ok := <-notifications:
			if !ok {
				return sessionResult{stable: stable, category: model.ErrorCodexUnavailable}
			}
			_, category := c.refresh(ctx, client)
			if category != "" {
				return sessionResult{stable: stable, category: category}
			}
		case <-poll.C:
			ok, category := c.refresh(ctx, client)
			stable = stable || ok
			if category != "" {
				return sessionResult{stable: stable, category: category}
			}
		case <-stat.C:
			current, _ := c.stat(c.cfg.AuthPath)
			if current != baseline {
				return sessionResult{stable: stable, immediate: true}
			}
		case <-recycle.C:
			return sessionResult{stable: stable, immediate: true}
		}
	}
}

func (c *Collector) refresh(ctx context.Context, client appServer) (bool, string) {
	requestCtx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	account, err := client.Account(requestCtx)
	cancel()
	if err != nil {
		return false, accountErrorCategory(err)
	}

	snapshot := snapshotForAccount(c.cfg.Username, c.now().UTC(), account)
	snapshot.CodexVersion = c.codexVersion
	if snapshot.State == model.StateUnavailable && snapshot.ErrorCategory == model.ErrorProtocol {
		return false, model.ErrorProtocol
	}
	if snapshot.State == model.StateOK {
		requestCtx, cancel = context.WithTimeout(ctx, c.cfg.RequestTimeout)
		limits, err := client.RateLimits(requestCtx)
		cancel()
		if err != nil {
			if errors.Is(err, codex.ErrProtocol) {
				return false, model.ErrorProtocol
			}
			return false, model.ErrorRateLimitRead
		}
		snapshot.Limits = sanitizeLimits(limits)
		snapshot.MainUsage = sanitizeMainUsage(limits)
		snapshot.ResetCreditsAvailable = sanitizeResetCredits(limits)

		// Usage and thread metadata are optional enhancements. Their failure
		// must never discard an otherwise valid quota observation.
		requestCtx, cancel = context.WithTimeout(ctx, c.cfg.RequestTimeout)
		usage, usageErr := client.AccountUsage(requestCtx)
		cancel()
		if usageErr == nil {
			snapshot.LifetimeTokensRead = true
			snapshot.LifetimeTokens = sanitizeLifetimeTokens(usage)
		}
		requestCtx, cancel = context.WithTimeout(ctx, c.cfg.RequestTimeout)
		threads, threadsErr := client.Threads(requestCtx, model.MaxRecentThreads)
		cancel()
		if threadsErr == nil {
			snapshot.RecentThreadsRead = true
			snapshot.RecentThreads = sanitizeRecentThreads(threads)
		}

		// Runtime status must come from the already-running user's control
		// daemon. A fresh collector App Server sees other processes as
		// notLoaded. The Hub must replace (and therefore clear) its previous
		// runtime set on every healthy snapshot, including when this optional
		// read fails; retaining an old set would create false active chats.
		requestCtx, cancel = context.WithTimeout(ctx, c.cfg.RequestTimeout)
		expectedEmail := ""
		if snapshot.Account != nil && snapshot.Account.Email != nil {
			expectedEmail = *snapshot.Account.Email
		}
		runtimeThreads, runtimeErr := c.runtimeThreads(requestCtx, expectedEmail)
		cancel()
		if runtimeErr == nil {
			snapshot.RuntimeThreadsRead = true
			snapshot.RuntimeThreads = sanitizeRuntimeThreads(runtimeThreads)
		}
	}
	snapshot.Normalize()
	if err := snapshot.Validate(); err != nil {
		return false, model.ErrorProtocol
	}
	publishCtx, cancel := context.WithTimeout(ctx, c.cfg.PublishTimeout)
	err = c.publish(publishCtx, snapshot)
	cancel()
	if err != nil {
		c.logCategory(model.ErrorPublish)
	}
	return true, ""
}

func sameChatGPTAccount(response codex.AccountResponse, expectedEmail string) bool {
	if response.Account == nil || response.Account.Type != "chatgpt" ||
		response.Account.Email == nil {
		return false
	}
	expected := strings.ToLower(strings.TrimSpace(expectedEmail))
	actual := strings.ToLower(strings.TrimSpace(*response.Account.Email))
	return expected != "" && actual == expected
}

func (c *Collector) publishUnavailable(ctx context.Context, category string) bool {
	snapshot := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      c.cfg.Username,
		CodexVersion:  c.codexVersion,
		State:         model.StateUnavailable,
		Limits:        []model.RateLimit{},
		ObservedAt:    c.now().UTC(),
		ErrorCategory: category,
	}
	publishCtx, cancel := context.WithTimeout(ctx, c.cfg.PublishTimeout)
	err := c.publish(publishCtx, snapshot)
	cancel()
	if err != nil {
		c.logCategory(model.ErrorPublish)
		return false
	}
	return true
}

func (c *Collector) publishSignedOut(ctx context.Context) bool {
	snapshot := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      c.cfg.Username,
		CodexVersion:  c.codexVersion,
		State:         model.StateSignedOut,
		Limits:        []model.RateLimit{},
		ObservedAt:    c.now().UTC(),
	}
	publishCtx, cancel := context.WithTimeout(ctx, c.cfg.PublishTimeout)
	err := c.publish(publishCtx, snapshot)
	cancel()
	if err != nil {
		c.logCategory(model.ErrorPublish)
		return false
	}
	return true
}

// probeCodexVersion executes the same configured, pinned CLI path used for
// the collector's App Server. It runs once per collector process, needs no
// authentication input, discards stderr, and retains only a short version
// token from stdout.
func (c *Collector) probeCodexVersion(ctx context.Context) {
	timeout := min(c.cfg.RequestTimeout, maxVersionProbeTime)
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	version, err := c.detectVersion(probeCtx, c.cfg.CodexPath)
	if err == nil {
		c.codexVersion = model.SanitizeCodexVersion(version)
	}
}

type boundedVersionOutput struct {
	bytes    []byte
	limit    int
	exceeded bool
}

func (output *boundedVersionOutput) Write(data []byte) (int, error) {
	remaining := output.limit - len(output.bytes)
	if remaining > 0 {
		amount := min(remaining, len(data))
		output.bytes = append(output.bytes, data[:amount]...)
	}
	if len(data) > remaining {
		output.exceeded = true
	}
	// Always report the full write so os/exec keeps draining a noisy child
	// without retaining unbounded output in dashboard memory.
	return len(data), nil
}

func detectCodexVersion(ctx context.Context, path string) (string, error) {
	output := boundedVersionOutput{limit: maxVersionOutputBytes}
	command := exec.CommandContext(ctx, path, "--version")
	command.Stdout = &output
	command.Stderr = io.Discard
	command.WaitDelay = maxVersionProbeCleanup
	if err := command.Run(); err != nil || output.exceeded {
		return "", errors.New("codex version probe failed")
	}
	return parseCodexVersionOutput(output.bytes)
}

func parseCodexVersionOutput(output []byte) (string, error) {
	fields := strings.Fields(string(output))
	var version string
	switch {
	case len(fields) == 2 && (strings.EqualFold(fields[0], "codex-cli") || strings.EqualFold(fields[0], "codex")):
		version = fields[1]
	default:
		return "", errors.New("codex version output is invalid")
	}
	version = model.SanitizeCodexVersion(version)
	if version == "" {
		return "", errors.New("codex version output is invalid")
	}
	return version, nil
}

func (c *Collector) logCategory(category string) {
	if c.cfg.Logger == nil {
		return
	}
	c.logMu.Lock()
	c.cfg.Logger.Printf("collector category=%s", category)
	c.logMu.Unlock()
}

func accountErrorCategory(err error) string {
	if errors.Is(err, codex.ErrProtocol) {
		return model.ErrorProtocol
	}
	var rpcErr *codex.RPCError
	if errors.As(err, &rpcErr) {
		return model.ErrorAuthUnavailable
	}
	return model.ErrorCodexUnavailable
}

func clientErrorCategory(err error) string {
	if errors.Is(err, codex.ErrProtocol) {
		return model.ErrorProtocol
	}
	return model.ErrorCodexUnavailable
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextBackoff(current, maximum time.Duration) time.Duration {
	if current >= maximum {
		return maximum
	}
	if current > maximum/2 {
		return maximum
	}
	return current * 2
}

type authMetadata struct {
	Known   bool
	Exists  bool
	Size    int64
	Mode    os.FileMode
	ModTime int64
	Inode   uint64
}

func statAuthMetadata(path string) (authMetadata, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return authMetadata{Known: true}, nil
	}
	if err != nil {
		return authMetadata{}, err
	}
	metadata := authMetadata{
		Known:   true,
		Exists:  true,
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime().UnixNano(),
	}
	metadata.Inode = inodeOf(info)
	return metadata, nil
}
