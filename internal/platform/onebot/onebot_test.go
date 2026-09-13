package onebot

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
)

type handlerFunc func(context.Context, messaging.InboundMessage) (platform.HandleResult, error)

func (f handlerFunc) HandleInbound(ctx context.Context, msg messaging.InboundMessage) (platform.HandleResult, error) {
	return f(ctx, msg)
}

func testConfig(apiURL string) Config {
	return Config{
		ID: "qq-main", APIURL: apiURL, WebhookAddr: "127.0.0.1:0", WebhookPath: "/onebot",
		RequestLimit: 1 << 20, Timeout: time.Second, ReplayTTL: time.Minute, ReplayMax: 100,
		Groups: map[string]string{"8": "support"},
	}
}

func TestWebhookMapsGroupMessageAndAuth(t *testing.T) {
	cfg := testConfig("http://127.0.0.1:5700")
	cfg.ReceiveSecret = "secret"
	a, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got messaging.InboundMessage
	h := handlerFunc(func(_ context.Context, msg messaging.InboundMessage) (platform.HandleResult, error) {
		got = msg
		return platform.HandleResult{Accepted: true, Verdict: "pass"}, nil
	})
	body := mustJSON(t, map[string]any{
		"post_type": "message", "message_type": "group", "time": int64(1700000000),
		"message_id": 42, "user_id": 7, "group_id": 8, "self_id": 99, "raw_message": "hello [CQ:at,qq=99]",
		"sender": map[string]any{"nickname": "alice"},
	})
	req := httptest.NewRequest(http.MethodPost, "/onebot", bytes.NewReader(body))
	req.Header.Set("X-Signature", sign(body, "secret"))
	rr := httptest.NewRecorder()
	a.webhook(h)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got.ConversationID != "support" || got.UserID != "7" || got.MessageID != "42" || !got.Mentioned || got.UserName != "alice" {
		t.Fatalf("unexpected message: %+v", got)
	}
}

func TestWebhookRejectsInvalidToken(t *testing.T) {
	cfg := testConfig("http://127.0.0.1:5700")
	cfg.ReceiveSecret = "secret"
	a, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	a.webhook(handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		t.Fatal("handler called")
		return platform.HandleResult{}, nil
	}))(rr, httptest.NewRequest(http.MethodPost, "/onebot", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestWebhookProcessingContextSurvivesRequestCancellation(t *testing.T) {
	adapter, err := New(testConfig("http://127.0.0.1:5700"), nil)
	if err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, map[string]any{
		"post_type": "message", "message_type": "group", "message_id": 43,
		"user_id": 7, "group_id": 8, "self_id": 99, "raw_message": "hello",
	})
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/onebot", bytes.NewReader(body)).WithContext(ctx)
	cancel()
	called := false
	response := httptest.NewRecorder()
	adapter.webhook(handlerFunc(func(ctx context.Context, _ messaging.InboundMessage) (platform.HandleResult, error) {
		called = true
		if err := ctx.Err(); err != nil {
			t.Fatalf("processing context was canceled: %v", err)
		}
		return platform.HandleResult{Accepted: true, Verdict: "pass"}, nil
	}))(response, req)
	if !called {
		t.Fatal("handler was not called")
	}
}

func TestSenderUsesSendMsgAndReplySegments(t *testing.T) {
	var got map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/send_msg" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0}`))
	}))
	defer api.Close()
	a, err := New(testConfig(api.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = a.Sender().SendTo(context.Background(), "8", messaging.OutboundMessage{ConversationID: "support", ReplyToMessageID: "42", Text: "reply"})
	if err != nil {
		t.Fatal(err)
	}
	if got["group_id"] != float64(8) || got["message_type"] != "group" {
		t.Fatalf("unexpected payload: %#v", got)
	}
	segments, ok := got["message"].([]any)
	if !ok || len(segments) != 2 {
		t.Fatalf("expected reply segments: %#v", got["message"])
	}
}

func TestWebhookIgnoresUnboundGroup(t *testing.T) {
	a, err := New(testConfig("http://127.0.0.1:5700"), nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	body := mustJSON(t, map[string]any{"post_type": "message", "message_type": "group", "message_id": 1, "user_id": 2, "group_id": 999, "raw_message": "ignored"})
	a.webhook(handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		t.Fatal("handler called")
		return platform.HandleResult{}, nil
	}))(rr, httptest.NewRequest(http.MethodPost, "/onebot", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestWebhookRejectsReplayWithoutCallingHandlerAgain(t *testing.T) {
	adapter, err := New(testConfig("http://127.0.0.1:5700"), nil)
	if err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, map[string]any{
		"post_type": "message", "message_type": "group", "message_id": 42,
		"user_id": 7, "group_id": 8, "self_id": 99, "raw_message": "/restart",
	})
	calls := 0
	handler := handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		calls++
		return platform.HandleResult{Accepted: true}, nil
	})
	for range 2 {
		response := httptest.NewRecorder()
		adapter.webhook(handler)(response, httptest.NewRequest(http.MethodPost, "/onebot", bytes.NewReader(body)))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d", calls)
	}
}

func TestMessageSegmentsProduceCleanText(t *testing.T) {
	event := rawEvent{
		PostType: "message", MessageType: "group", MessageID: json.RawMessage(`42`),
		UserID: json.RawMessage(`7`), GroupID: json.RawMessage(`8`), SelfID: json.RawMessage(`99`),
		RawMessage: "[CQ:at,qq=99] /status",
		Message:    json.RawMessage(`[{"type":"at","data":{"qq":"99"}},{"type":"text","data":{"text":" /status"}}]`),
	}
	message, err := event.message(map[string]string{"8": "ops"})
	if err != nil {
		t.Fatal(err)
	}
	if message.Text != "/status" || !message.Command || !message.Mentioned {
		t.Fatalf("message = %+v", message)
	}
}

func TestReplayCacheExpiresAndEvicts(t *testing.T) {
	cache := newReplayCache(time.Second, 2)
	now := time.Unix(100, 0)
	if cache.seenOrAdd("a", now) || !cache.seenOrAdd("a", now) {
		t.Fatal("duplicate was not detected")
	}
	if cache.seenOrAdd("b", now) || cache.seenOrAdd("c", now) {
		t.Fatal("new key was rejected")
	}
	if cache.seenOrAdd("a", now) {
		t.Fatal("oldest key was not evicted")
	}
	if cache.seenOrAdd("a", now.Add(2*time.Second)) {
		t.Fatal("expired key was treated as replay")
	}
}

func TestReplayCacheAllowsOnlyOneConcurrentFirstSeen(t *testing.T) {
	cache := newReplayCache(time.Minute, 100)
	now := time.Unix(100, 0)
	var firstSeen atomic.Int32
	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			if !cache.seenOrAdd("same-event", now) {
				firstSeen.Add(1)
			}
		}()
	}
	group.Wait()
	if got := firstSeen.Load(); got != 1 {
		t.Fatalf("first seen count = %d", got)
	}
}

func TestMessageSegmentsProduceCleanTextAndMention(t *testing.T) {
	event := rawEvent{
		PostType: "message", MessageType: "group", MessageID: json.RawMessage("42"),
		UserID: json.RawMessage("7"), GroupID: json.RawMessage("8"), SelfID: json.RawMessage("99"),
		RawMessage: "[CQ:at,qq=99] hello [CQ:image,file=x]",
		Message: mustJSON(t, []map[string]any{
			{"type": "at", "data": map[string]string{"qq": "99"}},
			{"type": "text", "data": map[string]string{"text": " hello "}},
			{"type": "image", "data": map[string]string{"file": "x"}},
		}),
	}
	message, err := event.message(map[string]string{"8": "support"})
	if err != nil {
		t.Fatal(err)
	}
	if message.Text != "hello" || !message.Mentioned {
		t.Fatalf("message = %+v", message)
	}
}

func TestMessageExtractsReplyRelationship(t *testing.T) {
	event := rawEvent{
		PostType: "message", MessageType: "group", MessageID: json.RawMessage("42"),
		UserID: json.RawMessage("7"), GroupID: json.RawMessage("8"), SelfID: json.RawMessage("99"),
		RawMessage: "[CQ:reply,id=123] answer",
		Message: mustJSON(t, []map[string]any{
			{"type": "reply", "data": map[string]any{"id": 123}},
			{"type": "text", "data": map[string]string{"text": " answer"}},
		}),
	}
	message, err := event.message(map[string]string{"8": "support"})
	if err != nil {
		t.Fatal(err)
	}
	if message.ReplyToMessageID != "123" || message.Text != "answer" {
		t.Fatalf("message = %+v", message)
	}
}

func TestMessageExtractsReplyRelationshipFromCQFallback(t *testing.T) {
	event := rawEvent{
		PostType: "message", MessageType: "group", MessageID: json.RawMessage("42"),
		UserID: json.RawMessage("7"), GroupID: json.RawMessage("8"),
		RawMessage: "[CQ:reply,id=123] answer", Message: json.RawMessage(`"answer"`),
	}
	message, err := event.message(map[string]string{"8": "support"})
	if err != nil {
		t.Fatal(err)
	}
	if message.ReplyToMessageID != "123" || message.Text != "answer" {
		t.Fatalf("message = %+v", message)
	}
}

func TestAdapterStartIsReadyAndCanRestartAfterStop(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig("http://127.0.0.1:5700")
	cfg.WebhookAddr = addr
	adapter, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		return platform.HandleResult{}, nil
	})
	for cycle := 0; cycle < 2; cycle++ {
		if err := adapter.Start(context.Background(), handler); err != nil {
			t.Fatalf("start cycle %d: %v", cycle, err)
		}
		probe, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			t.Fatalf("listener not ready on cycle %d: %v", cycle, err)
		}
		_ = probe.Close()
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = adapter.Stop(stopCtx)
		cancel()
		if err != nil {
			t.Fatalf("stop cycle %d: %v", cycle, err)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sign(body []byte, secret string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha1=" + hex.EncodeToString(mac.Sum(nil))
}
