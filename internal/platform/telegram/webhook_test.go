package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
)

const testSecret = "hook_secret-1"

func webhookAdapter(t *testing.T) *Adapter {
	t.Helper()
	a := testAdapter(t)
	a.cfg.Mode, a.cfg.WebhookURL, a.cfg.WebhookAddr, a.cfg.WebhookPath, a.cfg.WebhookSecret = ModeWebhook, "https://bot.example.com/tg", "127.0.0.1:0", "/tg", testSecret
	return a
}

type platformCalls struct {
	mu      sync.Mutex
	methods []string
	bodies  []string
}

func (c *platformCalls) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.methods...)
}

// webhookTransport 记录平台调用；setWebhook 的结果由 register 决定。
func webhookTransport(t *testing.T, calls *platformCalls, register func() (*http.Response, error)) roundTrip {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var body bytes.Buffer
		_, _ = body.ReadFrom(r.Body)
		calls.mu.Lock()
		calls.methods = append(calls.methods, method)
		calls.bodies = append(calls.bodies, body.String())
		calls.mu.Unlock()
		switch method {
		case "getMe":
			return response(200, `{"ok":true,"result":{"id":99,"is_bot":true,"username":"ForgeBot"}}`), nil
		case "getChat":
			return response(200, `{"ok":true,"result":{"id":-100,"type":"supergroup"}}`), nil
		case "setWebhook":
			return register()
		case "deleteWebhook", "getChatAdministrators":
			if method == "getChatAdministrators" {
				return response(200, `{"ok":true,"result":[]}`), nil
			}
			return response(200, `{"ok":true,"result":true}`), nil
		case "getUpdates":
			t.Error("webhook mode polled getUpdates")
		}
		return nil, errors.New("unexpected method " + method)
	}
}

func postUpdate(t *testing.T, a *Adapter, secret string, item any) int {
	t.Helper()
	a.mu.Lock()
	address := a.run.listener.Addr().String()
	a.mu.Unlock()
	data, _ := json.Marshal(item)
	request, _ := http.NewRequest(http.MethodPost, "http://"+address+"/tg", bytes.NewReader(data))
	if secret != "" {
		request.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	}
	result, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = result.Body.Close()
	return result.StatusCode
}

func TestWebhookRegistersAuthenticatesQueuesAndUnregisters(t *testing.T) {
	a := webhookAdapter(t)
	calls := &platformCalls{}
	a.client.Transport = webhookTransport(t, calls, func() (*http.Response, error) {
		return response(200, `{"ok":true,"result":true}`), nil
	})
	got := make(chan messaging.InboundMessage, 2)
	if err := a.Start(context.Background(), handlerFunc(func(_ context.Context, m messaging.InboundMessage) (platform.HandleResult, error) {
		got <- m
		return platform.HandleResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if methods := strings.Join(calls.list(), ","); methods != "getMe,getChat,setWebhook" {
		t.Fatal("registration order", methods)
	}
	calls.mu.Lock()
	register := calls.bodies[2]
	calls.mu.Unlock()
	for _, want := range []string{`"secret_token":"` + testSecret + `"`, `"drop_pending_updates":false`, `"max_connections":1`, `"allowed_updates":["message","my_chat_member"]`} {
		if !strings.Contains(register, want) {
			t.Fatal("setWebhook missing", want, register)
		}
	}
	m := testMessage()
	m.Text = "@ForgeBot hi"
	m.Entities = []entity{{Type: "mention", Offset: 0, Length: 9}}
	item := update{ID: 7, Message: &m}
	for _, secret := range []string{"", "wrong"} {
		if status := postUpdate(t, a, secret, item); status != http.StatusUnauthorized {
			t.Fatal("unauthenticated update accepted", status)
		}
	}
	if status := postUpdate(t, a, testSecret, item); status != http.StatusOK {
		t.Fatal(status)
	}
	if status := postUpdate(t, a, testSecret, item); status != http.StatusOK {
		t.Fatal("duplicate redelivery not acknowledged", status)
	}
	select {
	case message := <-got:
		if !message.Mentioned || message.Text != "hi" {
			t.Fatal(message)
		}
	case <-time.After(time.Second):
		t.Fatal("webhook update not handled")
	}
	select {
	case <-got:
		t.Fatal("duplicate update handled twice")
	case <-time.After(50 * time.Millisecond):
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	methods := calls.list()
	if methods[len(methods)-1] != "deleteWebhook" {
		t.Fatal("stop did not unregister", methods)
	}
	calls.mu.Lock()
	unregister := calls.bodies[len(calls.bodies)-1]
	calls.mu.Unlock()
	if !strings.Contains(unregister, `"drop_pending_updates":false`) {
		t.Fatal("unregister dropped pending updates", unregister)
	}
}

func TestWebhookQueueFullReturnsRetryableStatusWithoutClaiming(t *testing.T) {
	a := webhookAdapter(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &receiver{ctx: ctx, cancel: cancel, queue: make(chan update, 1)}
	r.queue <- update{ID: 1}
	m := testMessage()
	data, _ := json.Marshal(update{ID: 5, Message: &m})
	for _, tc := range []struct {
		name string
		body []byte
		want int
	}{
		{"full", data, http.StatusServiceUnavailable},
		{"invalid", []byte(`{`), http.StatusBadRequest},
		{"oversized", append(data, bytes.Repeat([]byte(" "), maxWebhookBody)...), http.StatusRequestEntityTooLarge},
	} {
		request, _ := http.NewRequest(http.MethodPost, "http://local/tg", bytes.NewReader(tc.body))
		request.Header.Set("X-Telegram-Bot-Api-Secret-Token", testSecret)
		recorder := &responseRecorder{header: http.Header{}}
		a.webhook(r)(recorder, request)
		if recorder.status != tc.want {
			t.Fatal(tc.name, recorder.status)
		}
	}
	if _, claimed := a.replays[5]; claimed || a.offset != 0 {
		t.Fatal("rejected update was claimed")
	}
	cancel()
	<-r.queue
	request, _ := http.NewRequest(http.MethodPost, "http://local/tg", bytes.NewReader(data))
	request.Header.Set("X-Telegram-Bot-Api-Secret-Token", testSecret)
	recorder := &responseRecorder{header: http.Header{}}
	a.webhook(r)(recorder, request)
	if recorder.status != http.StatusServiceUnavailable {
		t.Fatal("stopped receiver acknowledged update", recorder.status)
	}
}

type responseRecorder struct {
	header http.Header
	status int
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return len(b), nil
}
func (r *responseRecorder) WriteHeader(status int) { r.status = status }

func TestWebhookStartFailureReleasesListenerAndUnregistersUnknown(t *testing.T) {
	for _, mode := range []string{"rejected", "unknown", "port"} {
		t.Run(mode, func(t *testing.T) {
			a := webhookAdapter(t)
			if mode == "port" {
				occupied, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer occupied.Close()
				a.cfg.WebhookAddr = occupied.Addr().String()
			} else {
				free, _ := net.Listen("tcp", "127.0.0.1:0")
				a.cfg.WebhookAddr = free.Addr().String()
				_ = free.Close()
			}
			calls := &platformCalls{}
			a.client.Transport = webhookTransport(t, calls, func() (*http.Response, error) {
				if mode == "unknown" {
					return response(503, `{"ok":false,"error_code":503}`), nil
				}
				return response(401, `{"ok":false,"error_code":401}`), nil
			})
			err := a.Start(context.Background(), handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
				return platform.HandleResult{}, nil
			}))
			if err == nil || a.run != nil {
				t.Fatal("failed start left receiver", err)
			}
			methods := strings.Join(calls.list(), ",")
			switch mode {
			case "port":
				if strings.Contains(methods, "setWebhook") || operation.Describe(err) == nil && !strings.Contains(err.Error(), "address") {
					t.Fatal("registered before binding port", methods, err)
				}
			case "unknown":
				if !strings.HasSuffix(methods, "setWebhook,deleteWebhook") {
					t.Fatal("unknown registration not revoked", methods)
				}
			case "rejected":
				if strings.Contains(methods, "deleteWebhook") {
					t.Fatal("rejected registration revoked needlessly", methods)
				}
			}
			if mode != "port" {
				listener, err := net.Listen("tcp", a.cfg.WebhookAddr)
				if err != nil {
					t.Fatal("listener not released", err)
				}
				_ = listener.Close()
			}
		})
	}
}

func TestStopReportsLocalSuccessWhenRemoteUnregisterFails(t *testing.T) {
	a := webhookAdapter(t)
	calls := &platformCalls{}
	registered := false
	base := webhookTransport(t, calls, func() (*http.Response, error) {
		registered = true
		return response(200, `{"ok":true,"result":true}`), nil
	})
	a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if registered && strings.HasSuffix(r.URL.Path, "/deleteWebhook") {
			return nil, errors.New("network down")
		}
		return base(r)
	})
	if err := a.Start(context.Background(), handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		return platform.HandleResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatal("remote unregister failure reported as local stop failure", err)
	}
	// 同一实例可重新启动，并按当前模式重新登记。
	if err := a.Start(context.Background(), handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		return platform.HandleResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(context.Background())
	if count := strings.Count(strings.Join(calls.list(), ","), "setWebhook"); count != 2 {
		t.Fatal("restart did not register again", count)
	}
}
