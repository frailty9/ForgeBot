// Package telegram 实现 Telegram Bot API 群聊接入。
package telegram

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
)

type Group struct{ ConversationID, PromptSuffix string }

type Config struct {
	ID, Token, PromptSuffix string
	Timeout                 time.Duration
	Groups                  map[int64]Group
}

type receiver struct {
	ctx    context.Context
	cancel context.CancelFunc
	queue  chan update
	done   chan struct{}
	wg     sync.WaitGroup
}

type roleCache struct {
	until time.Time
	roles map[int64]string
}

type Adapter struct {
	cfg       Config
	logger    *slog.Logger
	client    *http.Client
	apiBase   string
	now       func() time.Time
	errors    chan error
	lifecycle sync.Mutex
	mu        sync.Mutex
	run       *receiver
	self      user
	offset    int64
	replays   map[int64]time.Time
	rolesMu   sync.Mutex
	roles     map[int64]roleCache
}

func New(cfg Config, logger *slog.Logger) (*Adapter, error) {
	if cfg.ID == "" || cfg.Token == "" || strings.ContainsAny(cfg.Token, "/?# \r\n\t") || cfg.Timeout <= 0 || cfg.Timeout > time.Duration(1<<63-1)-30*time.Second || len(cfg.Groups) == 0 {
		return nil, errors.New("invalid telegram adapter configuration")
	}
	if logger == nil {
		logger = slog.Default()
	}
	groups := make(map[int64]Group, len(cfg.Groups))
	for id, binding := range cfg.Groups {
		groups[id] = binding
	}
	cfg.Groups = groups
	return &Adapter{cfg: cfg, logger: logger, apiBase: "https://api.telegram.org", now: time.Now,
		client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		errors: make(chan error, 1), replays: map[int64]time.Time{}, roles: map[int64]roleCache{}}, nil
}

func (a *Adapter) ID() string                       { return a.cfg.ID }
func (a *Adapter) Sender() platform.ConnectorSender { return a }
func (a *Adapter) Errors() <-chan error             { return a.errors }
func (*Adapter) MessageSupport() group.MessageSupport {
	return group.MessageSupport{TextMessages: true}
}

// Start 每次重建接收资源；同实例保留已接纳的 offset 和短期去重标记。
func (a *Adapter) Start(ctx context.Context, handler platform.Handler) error {
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	if handler == nil {
		return errors.New("telegram handler is required")
	}
	a.mu.Lock()
	active := a.run != nil
	a.mu.Unlock()
	if active {
		return errors.New("telegram adapter already started")
	}
	runCtx, cancel := context.WithCancel(ctx)
	r := &receiver{ctx: runCtx, cancel: cancel, queue: make(chan update, 128), done: make(chan struct{})}
	ready := false
	defer func() {
		if !ready {
			cancel()
		}
	}()
	var self user
	if err := a.call(runCtx, "getMe", nil, &self, a.cfg.Timeout); err != nil {
		return err
	}
	if self.ID <= 0 || self.Username == "" || !self.IsBot {
		return operation.New(operation.Invalid, "invalid Telegram bot identity")
	}
	for id := range a.cfg.Groups {
		var target chat
		if err := a.call(runCtx, "getChat", map[string]any{"chat_id": id}, &target, a.cfg.Timeout); err != nil {
			return err
		}
		if target.ID != id || !target.supported() {
			return operation.New(operation.Unsupported, "Telegram binding requires a group or non-forum supergroup")
		}
	}
	if err := a.call(runCtx, "deleteWebhook", map[string]bool{"drop_pending_updates": false}, nil, a.cfg.Timeout); err != nil {
		return err
	}
	updates, err := a.getUpdates(runCtx, 0)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.run, a.self = r, self
	a.mu.Unlock()
	// 探测批次先接纳再启动读取循环；记账容量耗尽也必须能在启动预算内退出。
	acceptCtx, acceptCancel := context.WithTimeout(runCtx, a.cfg.Timeout)
	defer acceptCancel()
	for _, item := range updates {
		if err := a.accept(acceptCtx, r, item); err != nil {
			a.mu.Lock()
			a.run = nil
			a.mu.Unlock()
			return err
		}
	}
	for range 4 {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			for {
				select {
				case <-runCtx.Done():
					return
				case item := <-r.queue:
					if runCtx.Err() != nil {
						return
					}
					if err := a.handle(runCtx, handler, item); err != nil {
						a.logger.WarnContext(runCtx, "telegram message processing failed", "connector_id", a.cfg.ID, "failure", operation.Describe(err))
					}
				}
			}
		}()
	}
	r.wg.Add(1)
	go func() { defer r.wg.Done(); a.poll(r) }()
	go func() { r.wg.Wait(); close(r.done) }()
	ready = true
	return nil
}

func (a *Adapter) Stop(ctx context.Context) error {
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	a.mu.Lock()
	r := a.run
	a.mu.Unlock()
	if r == nil {
		return nil
	}
	r.cancel()
	select {
	case <-r.done:
		a.mu.Lock()
		a.run = nil
		a.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Adapter) requestContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	a.mu.Lock()
	r := a.run
	a.mu.Unlock()
	if r == nil {
		return ctx, cancel
	}
	stop := context.AfterFunc(r.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}
