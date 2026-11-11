// Package qqbot 实现 QQ 官方群聊 Webhook 和 REST，不依赖 OneBot。
package qqbot

import (
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
)

type Group struct{ ConversationID, PromptSuffix string }
type Config struct {
	ID, AppID, Secret, WebhookAddr, WebhookPath, PromptSuffix string
	Timeout, ReplayTTL                                        time.Duration
	RequestLimit                                              int64
	ReplayMax                                                 int
	// Sandbox 使用开放平台沙箱 OpenAPI，供尚未上线的机器人在沙箱群测试。
	Sandbox bool
	Groups  map[string]Group
}

type messageMeta struct {
	group, id, reference string
	at                   time.Time
	sequence             int
	bot                  bool
}

type Adapter struct {
	cfg        Config
	logger     *slog.Logger
	client     *http.Client
	tokenURL   string
	apiBase    string
	private    ed25519.PrivateKey
	now        func() time.Time
	errors     chan error
	lifecycle  sync.Mutex
	server     *http.Server
	listener   net.Listener
	cancel     context.CancelFunc
	runContext context.Context
	wg         sync.WaitGroup

	mu        sync.Mutex
	queue     chan messaging.InboundMessage
	replays   map[string]time.Time
	messages  map[string]messageMeta
	accepting bool

	tokenMu      sync.Mutex
	token        string
	tokenExpires time.Time
}

func New(cfg Config, logger *slog.Logger) (*Adapter, error) {
	if cfg.ID == "" || cfg.AppID == "" || cfg.Secret == "" || cfg.WebhookAddr == "" || !strings.HasPrefix(cfg.WebhookPath, "/") || cfg.Timeout <= 0 || cfg.ReplayTTL <= 0 || cfg.RequestLimit <= 0 || cfg.ReplayMax <= 0 || len(cfg.Groups) == 0 {
		return nil, errors.New("invalid qqbot adapter configuration")
	}
	if logger == nil {
		logger = slog.Default()
	}
	groups := make(map[string]Group, len(cfg.Groups))
	for target, group := range cfg.Groups {
		groups[target] = group
	}
	cfg.Groups = groups
	seed := cfg.Secret
	for len(seed) < ed25519.SeedSize {
		seed += seed
	}
	apiBase := "https://api.sgroup.qq.com"
	if cfg.Sandbox {
		apiBase = "https://sandbox.api.sgroup.qq.com"
	}
	return &Adapter{cfg: cfg, logger: logger, client: &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		tokenURL: "https://bots.qq.com/app/getAppAccessToken", apiBase: apiBase, private: ed25519.NewKeyFromSeed([]byte(seed[:ed25519.SeedSize])), now: time.Now, errors: make(chan error, 1), replays: map[string]time.Time{}, messages: map[string]messageMeta{}}, nil
}

func (a *Adapter) ID() string                       { return a.cfg.ID }
func (a *Adapter) Sender() platform.ConnectorSender { return a }
func (a *Adapter) Errors() <-chan error             { return a.errors }

func (*Adapter) MessageSupport() group.MessageSupport {
	return group.MessageSupport{TextMessages: true, PassiveRepliesOnly: true}
}

func (a *Adapter) Start(ctx context.Context, handler platform.Handler) error {
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	if a.server != nil {
		return errors.New("qqbot adapter already started")
	}
	runCtx, cancel := context.WithCancel(ctx)
	if _, err := a.accessToken(runCtx); err != nil {
		cancel()
		return err
	}
	listener, err := net.Listen("tcp", a.cfg.WebhookAddr)
	if err != nil {
		cancel()
		return err
	}
	queue := make(chan messaging.InboundMessage, 128)
	a.mu.Lock()
	a.queue, a.accepting, a.runContext = queue, true, runCtx
	a.mu.Unlock()
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+a.cfg.WebhookPath, a.webhook)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10}
	a.server, a.listener, a.cancel = server, listener, cancel
	for n := 0; n < 4; n++ {
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			for {
				select {
				case <-runCtx.Done():
					return
				case message := <-queue:
					if runCtx.Err() != nil {
						return
					}
					if _, err := handler.HandleInbound(runCtx, message); err != nil {
						a.logger.WarnContext(runCtx, "qqbot message processing failed", "connector_id", a.cfg.ID, "failure", operation.Describe(err))
					}
				}
			}
		}()
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case a.errors <- errors.New("qqbot webhook server failed"):
			default:
			}
		}
	}()
	return nil
}

func (a *Adapter) Stop(ctx context.Context) error {
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	if a.server == nil {
		return nil
	}
	a.mu.Lock()
	a.accepting = false
	a.mu.Unlock()
	a.cancel()
	err := a.server.Shutdown(ctx)
	if err != nil {
		_ = a.server.Close()
	}
	// 监听已关闭、工作者已取消；即使等待超时也释放句柄，否则恢复旧集合时 Start 会误判“已启动”。
	a.server, a.listener, a.cancel = nil, nil, nil
	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Adapter) pruneLocked(now time.Time) {
	for key, until := range a.replays {
		if !until.After(now) {
			delete(a.replays, key)
		}
	}
	for key, meta := range a.messages {
		if !meta.at.Add(5 * time.Minute).After(now) {
			delete(a.messages, key)
		}
	}
}

func messageKey(group, id string) string { return group + "\x00" + id }

func (a *Adapter) rememberLocked(meta messageMeta) {
	key := messageKey(meta.group, meta.id)
	if _, exists := a.messages[key]; !exists && len(a.messages) >= a.cfg.ReplayMax {
		oldestKey := ""
		var oldest messageMeta
		for candidate, value := range a.messages {
			if oldestKey == "" || value.at.Before(oldest.at) {
				oldestKey, oldest = candidate, value
			}
		}
		delete(a.messages, oldestKey)
	}
	a.messages[key] = meta
}

func (a *Adapter) context(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	a.mu.Lock()
	lifetime := a.runContext
	a.mu.Unlock()
	if lifetime == nil {
		return ctx, cancel
	}
	stop := context.AfterFunc(lifetime, cancel)
	return ctx, func() { stop(); cancel() }
}
