package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
)

type handlerFunc func(context.Context, messaging.InboundMessage) (platform.HandleResult, error)

func (f handlerFunc) HandleInbound(ctx context.Context, m messaging.InboundMessage) (platform.HandleResult, error) {
	return f(ctx, m)
}

// startupTransport 将账号/绑定检查留在真实启动路径，只有平台边界使用受控响应。
func startupTransport(t *testing.T, poll roundTrip) roundTrip {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:] {
		case "getMe":
			return response(200, `{"ok":true,"result":{"id":99,"is_bot":true,"username":"ForgeBot"}}`), nil
		case "getChat":
			return response(200, `{"ok":true,"result":{"id":-100,"type":"supergroup"}}`), nil
		case "deleteWebhook":
			var payload struct {
				Drop bool `json:"drop_pending_updates"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Drop {
				t.Error("pending updates dropped", err)
			}
			return response(200, `{"ok":true,"result":true}`), nil
		case "getChatAdministrators":
			return response(200, `{"ok":true,"result":[]}`), nil
		case "getUpdates":
			return poll(r)
		default:
			t.Errorf("unexpected call %s", r.URL.Path)
			return nil, errors.New("unexpected method")
		}
	}
}

type pollRequest struct {
	Offset  int64    `json:"offset"`
	Timeout int      `json:"timeout"`
	Limit   int      `json:"limit"`
	Allowed []string `json:"allowed_updates"`
}

func readPoll(t *testing.T, r *http.Request) pollRequest {
	t.Helper()
	var request pollRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Limit != 20 || strings.Join(request.Allowed, ",") != "message,my_chat_member" {
		t.Error(request, err)
	}
	return request
}

func TestStartProcessesProbeBatchAndRestartsWithAcceptedOffset(t *testing.T) {
	a := testAdapter(t)
	var probes atomic.Int32
	polled := make(chan int64, 4)
	a.client.Transport = startupTransport(t, func(r *http.Request) (*http.Response, error) {
		request := readPoll(t, r)
		if request.Timeout == 0 {
			if probes.Add(1) == 1 {
				m := testMessage()
				m.Text = "/status@ForgeBot"
				data, _ := json.Marshal(map[string]any{"ok": true, "result": []update{{ID: 5, Message: &m}, {ID: 50}}})
				return response(200, string(data)), nil
			}
			if request.Offset != 51 {
				t.Error("restart reset offset", request.Offset)
			}
			return response(200, `{"ok":true,"result":[]}`), nil
		}
		polled <- request.Offset
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	got := make(chan messaging.InboundMessage, 1)
	h := handlerFunc(func(_ context.Context, m messaging.InboundMessage) (platform.HandleResult, error) {
		got <- m
		return platform.HandleResult{}, nil
	})
	if err := a.Start(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(context.Background())
	select {
	case m := <-got:
		if m.Text != "/status" || !m.Command || m.ConversationID != "support" {
			t.Fatal(m)
		}
	case <-time.After(time.Second):
		t.Fatal("probe batch was lost")
	}
	select {
	case offset := <-polled:
		if offset != 51 {
			t.Fatal(offset)
		}
	case <-time.After(time.Second):
		t.Fatal("long poll did not start")
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background(), h); err != nil {
		t.Fatal(err)
	}
}

func TestQueueAndReplayCapacityWaitsCanBeCanceledWithoutConfirmation(t *testing.T) {
	for _, fullReplay := range []bool{false, true} {
		a := testAdapter(t)
		ctx, cancel := context.WithCancel(context.Background())
		r := &receiver{ctx: ctx, cancel: cancel, queue: make(chan update, 1)}
		m := testMessage()
		if fullReplay {
			for n := range 10000 {
				a.replays[int64(n+100)] = a.now().Add(time.Minute)
			}
		} else {
			r.queue <- update{ID: 99}
		}
		done := make(chan error, 1)
		go func() { done <- a.accept(r.ctx, r, update{ID: 5, Message: &m}) }()
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("full receiver did not cancel")
		}
		if a.offset != 0 {
			t.Fatal("unaccepted update confirmed", a.offset)
		}
		if _, exists := a.replays[5]; exists {
			t.Fatal("unaccepted update claimed")
		}
	}
}

func TestUpdateDeduplicationAndInvalidResponsesKeepOffset(t *testing.T) {
	a := testAdapter(t)
	r := &receiver{ctx: context.Background(), queue: make(chan update, 2)}
	m := testMessage()
	for range 2 {
		if err := a.accept(r.ctx, r, update{ID: 8, Message: &m}); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.queue) != 1 || a.offset != 9 {
		t.Fatal(len(r.queue), a.offset)
	}
	for _, body := range []string{`{`, `{"ok":false,"error_code":400}`, `{"ok":true,"result":{}}`, `{"ok":true,"result":[{"update_id":-1}]}`} {
		a.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return response(200, body), nil })
		if _, err := a.getUpdates(context.Background(), 0); err == nil || a.offset != 9 {
			t.Fatal(body, err, a.offset)
		}
	}
}

func TestPollingFatalErrorsAndRateDelayAreCancelable(t *testing.T) {
	for _, status := range []int{401, 403, 409, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			a := testAdapter(t)
			received := make(chan struct{}, 1)
			a.client.Transport = startupTransport(t, func(r *http.Request) (*http.Response, error) {
				if readPoll(t, r).Timeout == 0 {
					return response(200, `{"ok":true,"result":[]}`), nil
				}
				received <- struct{}{}
				return response(status, fmt.Sprintf(`{"ok":false,"error_code":%d,"parameters":{"retry_after":61}}`, status)), nil
			})
			if err := a.Start(context.Background(), handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
				return platform.HandleResult{}, nil
			})); err != nil {
				t.Fatal(err)
			}
			defer a.Stop(context.Background())
			<-received
			if status != 429 {
				select {
				case err := <-a.Errors():
					if operation.Describe(err).Code != operation.HTTPCode(status) {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("fatal poll error not reported")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := a.Stop(ctx); err != nil {
				t.Fatal("poll/backoff did not cancel", err)
			}
		})
	}
}

func TestStartRejectsUnsupportedBindingAndFailedProbeWithoutWorkers(t *testing.T) {
	for _, mode := range []string{"private", "channel", "forum", "probe", "credentials"} {
		t.Run(mode, func(t *testing.T) {
			a := testAdapter(t)
			base := startupTransport(t, func(*http.Request) (*http.Response, error) {
				return response(409, `{"ok":false,"error_code":409}`), nil
			})
			a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				if mode == "credentials" && strings.HasSuffix(r.URL.Path, "/getMe") {
					return response(401, `{"ok":false,"error_code":401}`), nil
				}
				if mode != "probe" && mode != "credentials" && strings.HasSuffix(r.URL.Path, "/getChat") {
					if mode == "forum" {
						return response(200, `{"ok":true,"result":{"id":-100,"type":"supergroup","is_forum":true}}`), nil
					}
					return response(200, fmt.Sprintf(`{"ok":true,"result":{"id":-100,"type":%q}}`, mode)), nil
				}
				return base(r)
			})
			if err := a.Start(context.Background(), handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
				return platform.HandleResult{}, nil
			})); err == nil || a.run != nil {
				t.Fatal(err, a.run)
			}
			if err := a.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStopCancelsBlockedWorkersAndFullQueue(t *testing.T) {
	a := testAdapter(t)
	a.client.Transport = startupTransport(t, func(r *http.Request) (*http.Response, error) {
		request := readPoll(t, r)
		first := max(request.Offset, 1)
		items := make([]update, 0, 20)
		for id := first; id < first+20; id++ {
			m := testMessage()
			m.ID = id
			items = append(items, update{ID: id, Message: &m})
		}
		data, _ := json.Marshal(map[string]any{"ok": true, "result": items})
		return response(200, string(data)), nil
	})
	if err := a.Start(context.Background(), handlerFunc(func(ctx context.Context, _ messaging.InboundMessage) (platform.HandleResult, error) {
		<-ctx.Done()
		return platform.HandleResult{}, ctx.Err()
	})); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(context.Background())
	deadline := time.Now().Add(3 * time.Second)
	for {
		a.mu.Lock()
		offset := a.offset
		a.mu.Unlock()
		if offset == 133 { // 四个工作者在处理，队列还接纳了 128 条。
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("receiver did not fill its queue", offset)
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Stop(ctx); err != nil {
		t.Fatal("Stop could not cancel queue backpressure", err)
	}
}

func TestProbeAdmissionCapacityFailureCleansUpStart(t *testing.T) {
	a := testAdapter(t)
	a.cfg.Timeout = 30 * time.Millisecond
	for n := range 10000 {
		a.replays[int64(n+100)] = a.now().Add(time.Minute)
	}
	a.client.Transport = startupTransport(t, func(*http.Request) (*http.Response, error) {
		m := testMessage()
		data, _ := json.Marshal(map[string]any{"ok": true, "result": []update{{ID: 1, Message: &m}}})
		return response(200, string(data)), nil
	})
	err := a.Start(context.Background(), handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		t.Error("failed Start dispatched a message")
		return platform.HandleResult{}, nil
	}))
	if !errors.Is(err, context.DeadlineExceeded) || a.run != nil || a.offset != 0 {
		t.Fatal("probe admission did not fail cleanly", err, a.run, a.offset)
	}
}
