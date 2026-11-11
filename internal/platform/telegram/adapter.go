// Package telegram 实现 Telegram Bot API 群聊接入。
package telegram

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
)

type Group struct{ ConversationID, PromptSuffix string }

const (
	ModeLongPolling = "long_polling"
	ModeWebhook     = "webhook"
)

type Config struct {
	ID, Token, PromptSuffix string
	Mode                    string
	// Webhook 模式专用：公网地址交给 setWebhook，本地监听与路径可经反向代理映射。
	WebhookURL, WebhookAddr, WebhookPath, WebhookSecret string
	Timeout                                             time.Duration
	Groups                                              map[int64]Group
}

type receiver struct {
	ctx      context.Context
	cancel   context.CancelFunc
	queue    chan update
	done     chan struct{}
	wg       sync.WaitGroup
	server   *http.Server
	listener net.Listener
}

// ValidWebhookSecret 按官方约束检查回调认证文本：1..256 个字母、数字、下划线或短横线。
func ValidWebhookSecret(secret string) bool {
	if len(secret) == 0 || len(secret) > 256 {
		return false
	}
	for _, ch := range secret {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}

// ValidWebhookURL 要求公网 HTTPS 地址、无用户信息与片段，端口限于平台支持的 443/80/88/8443。
func ValidWebhookURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(value) > 2048 {
		return false
	}
	switch u.Port() {
	case "", "443", "80", "88", "8443":
		return true
	}
	return false
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
	switch cfg.Mode {
	case ModeLongPolling:
	case ModeWebhook:
		if !ValidWebhookURL(cfg.WebhookURL) || cfg.WebhookAddr == "" || !strings.HasPrefix(cfg.WebhookPath, "/") || !ValidWebhookSecret(cfg.WebhookSecret) {
			return nil, errors.New("invalid telegram webhook configuration")
		}
	default:
		return nil, errors.New("telegram mode must be long_polling or webhook")
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

// GroupEventSupport：只提供新成员入群事件，不做 Telegram 自动入群审批。
func (*Adapter) GroupEventSupport() (bool, bool) { return false, true }

// Start 每次重建接收资源并按当前模式重新登记，不沿用“曾经登记过”的状态；
// 同实例保留已接纳的 offset 和短期去重标记。
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
	var updates []update
	if a.cfg.Mode == ModeWebhook {
		// 先占住本地端口，登记失败或端口占用都在通知平台前暴露。
		listener, err := net.Listen("tcp", a.cfg.WebhookAddr)
		if err != nil {
			return err
		}
		r.listener = listener
	} else {
		if err := a.call(runCtx, "deleteWebhook", map[string]bool{"drop_pending_updates": false}, nil, a.cfg.Timeout); err != nil {
			return err
		}
		var err error
		if updates, err = a.getUpdates(runCtx, 0); err != nil {
			return err
		}
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
	if a.cfg.Mode == ModeWebhook {
		if err := a.startWebhook(runCtx, r); err != nil {
			return err
		}
	} else {
		r.wg.Add(1)
		go func() { defer r.wg.Done(); a.poll(r) }()
	}
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
	// 接收已取消，立即释放句柄；否则等待超时后恢复旧集合时 Start 会误判“已启动”。
	a.mu.Lock()
	a.run = nil
	a.mu.Unlock()
	var localErr error
	if r.server != nil {
		if err := r.server.Shutdown(ctx); err != nil {
			_ = r.server.Close()
			localErr = err
		}
	}
	if localErr == nil {
		select {
		case <-r.done:
		case <-ctx.Done():
			localErr = ctx.Err()
		}
	}
	if a.cfg.Mode == ModeWebhook {
		// 本地已停止接收；远端撤销只尽力而为，失败不能伪装成本地 Stop 失败。
		a.unregisterWebhook()
	}
	return localErr
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
