package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/soleapi/forgebot/internal/operation"
)

const maxWebhookBody = 1 << 20

// startWebhook 在本地监听已就绪后才调用 setWebhook，平台登记失败时自行关闭监听。
// 不能依赖调用方为启动失败的实例调用 Stop。
func (a *Adapter) startWebhook(ctx context.Context, r *receiver) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+a.cfg.WebhookPath, a.webhook(r))
	r.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if err := r.server.Serve(r.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case a.errors <- errors.New("telegram webhook server failed"):
			default:
			}
		}
	}()
	err := a.call(ctx, "setWebhook", map[string]any{"url": a.cfg.WebhookURL, "secret_token": a.cfg.WebhookSecret,
		"allowed_updates": allowedUpdates, "max_connections": 1, "drop_pending_updates": false}, nil, a.cfg.Timeout)
	if err == nil {
		return nil
	}
	r.cancel()
	closeCtx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	_ = r.server.Shutdown(closeCtx)
	r.wg.Wait()
	a.mu.Lock()
	if a.run == r {
		a.run = nil
	}
	a.mu.Unlock()
	// 请求已发出但结果未知时，平台可能已登记；尽力撤销，不把错误叠加到启动失败上。
	if operation.Describe(err).Effect == operation.EffectUnknown {
		a.unregisterWebhook()
	}
	return err
}

// unregisterWebhook 用独立的有界上下文撤销登记，保留待处理更新；失败只记脱敏诊断。
func (a *Adapter) unregisterWebhook() {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	if err := a.call(ctx, "deleteWebhook", map[string]bool{"drop_pending_updates": false}, nil, a.cfg.Timeout); err != nil {
		a.logger.Warn("telegram webhook unregister not confirmed", "connector_id", a.cfg.ID, "failure", operation.Describe(err))
	}
}

// webhook 只有在更新入队或被明确忽略后才回 200；队列满或已停止回 503，交平台重投。
// Telegram 没有 QQ 式挑战，认证仅靠 secret_token 头。
func (a *Adapter) webhook(r *receiver) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		defer req.Body.Close()
		token := req.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if subtle.ConstantTimeCompare([]byte(token), []byte(a.cfg.WebhookSecret)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxWebhookBody))
		if err != nil {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		var item update
		if json.Unmarshal(body, &item) != nil || item.ID < 0 {
			http.Error(w, "invalid update", http.StatusBadRequest)
			return
		}
		if !a.tryAccept(r, item) {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
