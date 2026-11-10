package qqbot

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/history"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func testAdapter(t *testing.T) *Adapter {
	t.Helper()
	a, err := New(Config{ID: "qq", AppID: "app", Secret: "test-secret", WebhookAddr: "127.0.0.1:0", WebhookPath: "/qq", Timeout: time.Second, ReplayTTL: 5 * time.Minute, ReplayMax: 1000, RequestLimit: 1 << 20, Groups: map[string]Group{"group": {ConversationID: "support", PromptSuffix: "group-context"}}, PromptSuffix: "connector-context"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	a.queue = make(chan messaging.InboundMessage, 128)
	a.accepting = true
	a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/app/getAppAccessToken" {
			t.Errorf("unexpected API %s", r.URL.Path)
		}
		return response(200, `{"access_token":"token","expires_in":"7200"}`), nil
	})
	return a
}

func eventBody(a *Adapter, id, kind, role, content string) []byte {
	body, _ := json.Marshal(map[string]any{"op": 0, "t": kind, "id": "event-" + id, "d": map[string]any{
		"id": id, "group_openid": "group", "content": content, "timestamp": a.now().Format(time.RFC3339Nano),
		"author":        map[string]any{"member_openid": "user", "member_role": role, "username": "Name"},
		"message_scene": map[string]any{"ext": []string{"msg_idx=ref-" + id}},
	}})
	return body
}

func request(a *Adapter, body []byte) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://localhost/qq", strings.NewReader(string(body)))
	ts := strconv.FormatInt(a.now().Unix(), 10)
	r.Header.Set("X-Bot-Appid", a.cfg.AppID)
	r.Header.Set("X-Signature-Timestamp", ts)
	r.Header.Set("X-Signature-Ed25519", hex.EncodeToString(ed25519.Sign(a.private, append([]byte(ts), body...))))
	return r
}

func dispatch(t *testing.T, a *Adapter, body []byte, want int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	a.webhook(w, request(a, body))
	var ack struct{ Op, D int }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ack) != nil || ack.Op != 12 || ack.D != want {
		t.Fatalf("response=%d %s", w.Code, w.Body.String())
	}
	return w
}

func TestWebhookValidatesOriginalBytesAppAndAge(t *testing.T) {
	for _, mode := range []string{"body", "signature", "app", "old", "size"} {
		t.Run(mode, func(t *testing.T) {
			a := testAdapter(t)
			body := eventBody(a, "m", "GROUP_AT_MESSAGE_CREATE", "admin", "hi")
			r := request(a, body)
			switch mode {
			case "body":
				r.Body = io.NopCloser(strings.NewReader(string(body) + " "))
			case "signature":
				r.Header.Set("X-Signature-Ed25519", "00")
			case "app":
				r.Header.Set("X-Bot-Appid", "other")
			case "old":
				r.Header.Set("X-Signature-Timestamp", "1")
			case "size":
				a.cfg.RequestLimit = 2
			}
			w := httptest.NewRecorder()
			a.webhook(w, r)
			if w.Code == 200 || len(a.queue) != 0 {
				t.Fatalf("accepted %s: %d %s", mode, w.Code, w.Body.String())
			}
		})
	}
}

func TestChallengeAndHeartbeat(t *testing.T) {
	a := testAdapter(t)
	ts := strconv.FormatInt(a.now().Unix(), 10)
	body := []byte(`{"op":13,"d":{"plain_token":"challenge","event_ts":"` + ts + `"}}`)
	unsigned := request(a, body)
	unsigned.Header.Del("X-Signature-Ed25519")
	denied := httptest.NewRecorder()
	a.webhook(denied, unsigned)
	if denied.Code != http.StatusUnauthorized || strings.Contains(denied.Body.String(), "signature\":") {
		t.Fatal("unsigned challenge received a signature", denied.Code, denied.Body)
	}
	w := httptest.NewRecorder()
	a.webhook(w, request(a, body))
	var result struct {
		PlainToken string `json:"plain_token"`
		Signature  string `json:"signature"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.PlainToken != "challenge" {
		t.Fatal(w.Body.String())
	}
	sig, _ := hex.DecodeString(result.Signature)
	if !ed25519.Verify(a.private.Public().(ed25519.PublicKey), []byte(ts+"challenge"), sig) {
		t.Fatal("invalid validation signature")
	}
	w = httptest.NewRecorder()
	a.webhook(w, request(a, []byte(`{"op":1,"d":42}`)))
	if strings.TrimSpace(w.Body.String()) != `{"op":11,"d":42}` {
		t.Fatal(w.Body.String())
	}
}

func TestGroupNormalizationRoleMentionsDuplicatesAndQuoteMapping(t *testing.T) {
	a := testAdapter(t)
	dispatch(t, a, eventBody(a, "q", "GROUP_MESSAGE_CREATE", "member", "<@all> hello"), 0)
	q := <-a.queue
	if q.Mentioned || q.PlatformRole != "member" || q.MessageID != "q" || q.UserID != "user" || q.PromptSuffix != "connector-context\ngroup-context" {
		t.Fatal(q)
	}
	dispatch(t, a, eventBody(a, "q", "GROUP_AT_MESSAGE_CREATE", "member", "duplicate"), 0)
	if len(a.queue) != 0 {
		t.Fatal("duplicate message delivered")
	}
	body := eventBody(a, "a", "GROUP_AT_MESSAGE_CREATE", "owner", "answer")
	var decoded map[string]any
	_ = json.Unmarshal(body, &decoded)
	data := decoded["d"].(map[string]any)
	data["message_scene"] = map[string]any{"ext": []string{"ref_msg_idx=ref-q", "msg_idx=ref-a"}}
	data["msg_elements"] = []map[string]string{{"msg_idx": "ref-q", "content": "quoted question"}}
	data["attachments"] = []map[string]string{{"url": "https://example.com/image", "content_type": "image/png", "filename": "image"}}
	body, _ = json.Marshal(decoded)
	dispatch(t, a, body, 0)
	answer := <-a.queue
	if !answer.Mentioned || !answer.TrustedResponder() || answer.ReplyToMessageID != "q" || answer.Quoted == nil || answer.Quoted.Text != "quoted question" || len(answer.Media) != 1 || answer.Media[0].Kind != messaging.MediaImage {
		t.Fatalf("answer=%+v quote=%+v", answer, answer.Quoted)
	}
	dispatch(t, a, eventBody(a, "m", "GROUP_MESSAGE_CREATE", "forged-role", "ordinary"), 0)
	if (<-a.queue).TrustedResponder() {
		t.Fatal("unknown role became administrator")
	}
}

func TestQueueFullDoesNotClaimEventAndCapacityIsBounded(t *testing.T) {
	a := testAdapter(t)
	a.queue = make(chan messaging.InboundMessage, 1)
	a.cfg.ReplayMax = 2
	dispatch(t, a, eventBody(a, "first", "GROUP_MESSAGE_CREATE", "member", "first"), 0)
	dispatch(t, a, eventBody(a, "second", "GROUP_MESSAGE_CREATE", "member", "second"), 1)
	<-a.queue
	dispatch(t, a, eventBody(a, "second", "GROUP_MESSAGE_CREATE", "member", "second"), 0)
	<-a.queue
	dispatch(t, a, eventBody(a, "third", "GROUP_MESSAGE_CREATE", "member", "third"), 1)
	if len(a.replays) > 2 || len(a.messages) > 2 {
		t.Fatal("unbounded caches")
	}
}

func TestTokenRefreshIsSharedAndBuildDoesNotCallNetwork(t *testing.T) {
	a := testAdapter(t)
	calls := atomic.Int32{}
	a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["appId"] != "app" || body["clientSecret"] != "test-secret" {
			t.Error(body, err)
		}
		return response(200, `{"access_token":"shared","expires_in":7200}`), nil
	})
	if calls.Load() != 0 {
		t.Fatal("adapter construction called network")
	}
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := a.accessToken(context.Background())
			if err != nil || token != "shared" {
				t.Error(token, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent refresh count", calls.Load())
	}
	a.tokenMu.Lock()
	a.tokenExpires = a.now().Add(30 * time.Second)
	a.tokenMu.Unlock()
	_, _ = a.accessToken(context.Background())
	if calls.Load() != 2 {
		t.Fatal("near-expiry token was not refreshed")
	}
}

func TestPassiveSendSequencesAndConnectorLocalReferences(t *testing.T) {
	a := testAdapter(t)
	var sequences []int
	deletes := 0
	a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/app/getAppAccessToken" {
			return response(200, `{"access_token":"token","expires_in":7200}`), nil
		}
		if r.Header.Get("Authorization") != "QQBot token" {
			t.Error("wrong auth")
		}
		if r.Method == http.MethodDelete {
			deletes++
			return response(200, ""), nil
		}
		var body struct {
			ID        string `json:"msg_id"`
			Seq       int    `json:"msg_seq"`
			Reference struct {
				ID string `json:"message_id"`
			} `json:"message_reference"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID != "q" || body.Reference.ID != "ref-q" {
			t.Error(body, err)
		}
		sequences = append(sequences, body.Seq)
		return response(200, fmt.Sprintf(`{"id":"bot-%d","ext_info":{"ref_idx":"ref-bot-%d"}}`, body.Seq, body.Seq)), nil
	})
	dispatch(t, a, eventBody(a, "q", "GROUP_AT_MESSAGE_CREATE", "member", "question"), 0)
	<-a.queue
	for n := 1; n <= 5; n++ {
		if err := a.SendTo(context.Background(), "group", messaging.OutboundMessage{ReplyToMessageID: "q", Text: "answer"}); err != nil {
			t.Fatal(err)
		}
		if meta, ok := a.messages[messageKey("group", fmt.Sprintf("bot-%d", n))]; !ok || meta.reference != fmt.Sprintf("ref-bot-%d", n) {
			t.Fatal("connector-local reference mapping missing", meta)
		}
	}
	if err := a.SendTo(context.Background(), "group", messaging.OutboundMessage{ReplyToMessageID: "q", Text: "extra"}); operation.Describe(err).Code != operation.BudgetExhausted {
		t.Fatal(err)
	}
	if len(sequences) != 5 || sequences[0] != 1 || sequences[4] != 5 {
		t.Fatal(sequences)
	}
	if err := a.RecallMessage(context.Background(), "group", "bot-1"); err != nil || deletes != 1 {
		t.Fatal(err, deletes)
	}
	// 本地索引已删除也不阻止调用；平台决定权限与时限，适配器不代设限制。
	if err := a.RecallMessage(context.Background(), "group", "bot-1"); err != nil || deletes != 2 {
		t.Fatal(err, deletes)
	}
	if err := a.SendTo(context.Background(), "group", messaging.OutboundMessage{Text: "notification"}); operation.Describe(err).Code != operation.Unsupported {
		t.Fatal(err)
	}
	if err := a.RecallMessage(context.Background(), "foreign", "q"); operation.Describe(err).Code != operation.NotConfigured {
		t.Fatal(err)
	}
	a.mu.Lock()
	meta := a.messages[messageKey("group", "q")]
	meta.at = a.now().Add(-3 * time.Minute)
	a.messages[messageKey("group", "q")] = meta
	a.mu.Unlock()
	if err := a.RecallMessage(context.Background(), "group", "q"); err != nil || deletes != 3 {
		t.Fatal("local age must not block platform recall", err, deletes)
	}
}

func TestQQErrorsAreNonfatalAndNeverAutomaticallyResent(t *testing.T) {
	for _, tc := range []struct {
		body    string
		status  int
		network bool
		want    operation.Code
		effect  operation.Effect
	}{
		{`{"code":40062003}`, 400, false, operation.PermissionDenied, operation.NotStarted},
		{`{"code":40064004}`, 400, false, operation.Expired, operation.NotStarted},
		{`{"code":999}`, 400, false, operation.Invalid, operation.NotStarted},
		{"", 200, true, operation.Timeout, operation.EffectUnknown},
		{"not-json", 200, false, operation.Invalid, operation.EffectUnknown},
	} {
		t.Run(fmt.Sprint(tc.status, tc.body, tc.network), func(t *testing.T) {
			a := testAdapter(t)
			a.token = "cached"
			a.tokenExpires = a.now().Add(time.Hour)
			calls := 0
			a.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				if tc.network {
					return nil, context.DeadlineExceeded
				}
				return response(tc.status, tc.body), nil
			})
			err := a.api(context.Background(), http.MethodDelete, "/example", nil, nil)
			failure := operation.Describe(err)
			if failure == nil || failure.Code != tc.want || failure.Effect != tc.effect || calls != 1 {
				t.Fatal(failure, calls)
			}
			select {
			case err := <-a.Errors():
				t.Fatal("API error leaked into lifecycle", err)
			default:
			}
		})
	}
}

type handlerFunc func(context.Context, messaging.InboundMessage) (platform.HandleResult, error)

func (f handlerFunc) HandleInbound(ctx context.Context, m messaging.InboundMessage) (platform.HandleResult, error) {
	return f(ctx, m)
}

func TestWebhookACKDoesNotWaitForBusinessAndStopCancelsWork(t *testing.T) {
	a := testAdapter(t)
	entered := make(chan struct{})
	finished := make(chan struct{})
	if err := a.Start(context.Background(), handlerFunc(func(ctx context.Context, m messaging.InboundMessage) (platform.HandleResult, error) {
		close(entered)
		<-ctx.Done()
		close(finished)
		return platform.HandleResult{}, ctx.Err()
	})); err != nil {
		t.Fatal(err)
	}
	body := eventBody(a, "q", "GROUP_AT_MESSAGE_CREATE", "member", "question")
	r := request(a, body)
	r.URL.Scheme = "http"
	r.URL.Host = a.listener.Addr().String()
	r.RequestURI = ""
	client := &http.Client{Timeout: time.Second}
	res, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(data), `"d":0`) {
		t.Fatal(string(data))
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("in-flight work not canceled")
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestTokenFailureDoesNotActivateWebhook(t *testing.T) {
	a := testAdapter(t)
	a.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("unavailable") })
	if err := a.Start(context.Background(), handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		return platform.HandleResult{}, nil
	})); err == nil || a.server != nil {
		t.Fatal("bad token activated webhook", err)
	}
}

type answerClient struct{}

func (answerClient) Protocol() llm.Protocol { return llm.OpenAIResponses }
func (answerClient) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Text: "controlled answer"}, nil
}
func (answerClient) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

func TestControlledWebhookToRuntimeAnswer(t *testing.T) {
	a := testAdapter(t)
	sent := make(chan map[string]any, 1)
	a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/app/getAppAccessToken" {
			return response(200, `{"access_token":"token","expires_in":7200}`), nil
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		sent <- body
		return response(200, `{"id":"bot-response","ext_info":{"ref_idx":"bot-reference"}}`), nil
	})
	registry := platform.NewRegistry()
	_ = registry.RegisterConnector(a.ID(), a)
	_ = registry.BindConversation(platform.ConversationBinding{ConversationID: "support", ConnectorID: a.ID(), Target: "group"})
	snapshot := config.DefaultSnapshot()
	snapshot.Runtime.LLM.Model = "test"
	snapshot.Runtime.LLM.Authentication = config.AuthenticationNone
	snapshot.Runtime.Platforms = config.PlatformConfig{Connectors: []config.ConnectorConfig{{ID: a.ID(), Type: "qqbot", Enabled: true}}, Groups: []config.GroupBindingConfig{{ID: "support", ConnectorID: a.ID(), Target: "group"}}}
	window := history.New(history.Policy{}, []string{"support"}, nil)
	manager, err := runtimecfg.NewManager(context.Background(), snapshot, runtimecfg.Builder{Sender: registry, History: window, ClientFactory: func(llm.ClientConfig) (llm.Client, error) { return answerClient{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	if err := a.Start(context.Background(), manager); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(context.Background())
	dispatch(t, a, eventBody(a, "q", "GROUP_AT_MESSAGE_CREATE", "member", "question"), 0)
	select {
	case body := <-sent:
		if body["content"] != "controlled answer" || body["msg_id"] != "q" {
			t.Fatal(body)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not send answer")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, record := range window.Recent("support", 0) {
			if record.Role == history.RoleBot && record.ReplyTo == "q" && record.Text == "controlled answer" {
				if record.MessageID != "" {
					t.Fatal("platform ID leaked into host history", record)
				}
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("successful answer was not recorded by the existing host path")
}
