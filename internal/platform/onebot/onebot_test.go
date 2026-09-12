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
	return Config{ID: "qq-main", APIURL: apiURL, WebhookAddr: "127.0.0.1:0", WebhookPath: "/onebot", RequestLimit: 1 << 20, Timeout: time.Second, Groups: map[string]string{"8": "support"}}
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
