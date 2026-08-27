package collector

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
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
)

// Config controls one per-user collector. It contains paths and timings only;
// credentials are owned and read exclusively by the Codex subprocess.
type Config struct {
	Username string

	CodexPath  string
	SocketPath string
	AuthPath   string

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
	Notifications() <-chan string
	Done() <-chan struct{}
	Err() error
	Close() error
}

// Collector supervises one private Codex App Server and publishes allowlisted
// snapshots to the dashboard's local ingest socket.
type Collector struct {
	cfg Config

	start   func(context.Context) (appServer, error)
	publish func(context.Context, model.Snapshot) error
	stat    func(string) (authMetadata, error)
	now     func() time.Time

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

	c := &Collector{cfg: cfg, stat: statAuthMetadata, now: time.Now}
	c.start = func(ctx context.Context) (appServer, error) {
		return codex.Start(ctx, codex.Config{
			Path:             cfg.CodexPath,
			MaxLineBytes:     cfg.MaxRPCLine,
			HandshakeTimeout: cfg.RequestTimeout,
		})
	}
	c.publish = func(ctx context.Context, snapshot model.Snapshot) error {
		return publishUnix(ctx, cfg.SocketPath, cfg.MaxPayload, snapshot)
	}
	return c, nil
}

// Run supervises the app-server process. Expected cancellation is reported as
// success so systemd can stop the service cleanly.
func (c *Collector) Run(ctx context.Context) error {
	backoff := c.cfg.BackoffMin
	for {
		if ctx.Err() != nil {
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

func (c *Collector) publishUnavailable(ctx context.Context, category string) {
	snapshot := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		Username:      c.cfg.Username,
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
	}
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
