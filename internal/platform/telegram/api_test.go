package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func testAdapter(t *testing.T) *Adapter {
	t.Helper()
	a, err := New(Config{ID: "telegram", Token: "123:private-token", Mode: ModeLongPolling, Timeout: time.Second,
		Groups: map[int64]Group{-100: {ConversationID: "support", PromptSuffix: "group"}}, PromptSuffix: "connector"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	a.self = user{ID: 99, Username: "ForgeBot", IsBot: true}
	a.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected network call")
		return nil, errors.New("unexpected network call")
	})
	return a
}

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("secret URL must not leak") }
func (brokenBody) Close() error             { return nil }

func TestAPIValidatesHTTPEnvelopeAndNeverLeaksCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       operation.Code
		effect     operation.Effect
	}{
		{"http overrides ok", `{"ok":true,"result":true}`, 403, operation.PermissionDenied, operation.NotStarted},
		{"business error", `{"ok":false,"error_code":400,"description":"private-token"}`, 200, operation.Invalid, operation.NotStarted},
		{"rate limit", `{"ok":false,"error_code":429,"parameters":{"retry_after":61}}`, 200, operation.RateLimited, operation.NotStarted},
		{"migration", `{"ok":false,"error_code":400,"parameters":{"migrate_to_chat_id":-1001234567890}}`, 400, operation.Conflict, operation.NotStarted},
		{"server error", `{"ok":false,"error_code":503}`, 503, operation.Unavailable, operation.EffectUnknown},
		{"invalid json", `{`, 200, operation.Invalid, operation.EffectUnknown},
		{"oversized", `{"ok":true,"result":true}` + strings.Repeat(" ", maxResponseBytes), 200, operation.Invalid, operation.EffectUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testAdapter(t)
			calls := 0
			a.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				return response(tc.status, tc.body), nil
			})
			err := a.call(context.Background(), "example", nil, nil, a.cfg.Timeout)
			failure := operation.Describe(err)
			if failure == nil || failure.Code != tc.want || failure.Effect != tc.effect || calls != 1 || strings.Contains(err.Error(), a.cfg.Token) {
				t.Fatal(failure, err, calls)
			}
			if tc.name == "rate limit" && retryDelay(err, time.Second) != 61*time.Second {
				t.Fatal("retry_after was capped")
			}
			if tc.name == "migration" && !strings.Contains(err.Error(), "-1001234567890") {
				t.Fatal("migration target missing", err)
			}
		})
	}
	for _, bodyFailure := range []bool{false, true} {
		a := testAdapter(t)
		a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
			if bodyFailure {
				result := response(200, "")
				result.Body = brokenBody{}
				return result, nil
			}
			return nil, &url.Error{Op: "Post", URL: r.URL.String(), Err: context.DeadlineExceeded}
		})
		err := a.call(context.Background(), "example", nil, nil, a.cfg.Timeout)
		if err == nil || strings.Contains(err.Error(), a.cfg.Token) || strings.Contains(err.Error(), "secret URL") || operation.Describe(err).Effect != operation.EffectUnknown {
			t.Fatal(err)
		}
	}
}

func TestSendTextLimitsReplyAndRecallNoRetries(t *testing.T) {
	a := testAdapter(t)
	var methods []string
	var payloads []string
	a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		payloads = append(payloads, string(body))
		methods = append(methods, r.URL.Path)
		return response(200, `{"ok":true,"result":true}`), nil
	})
	for _, text := range []string{"", " ", strings.Repeat("😀", 4097), string([]byte{0xff})} {
		if err := a.SendTo(context.Background(), "-100", messaging.OutboundMessage{Text: text}); operation.Describe(err).Code != operation.Invalid {
			t.Fatal(err)
		}
	}
	if len(methods) != 0 {
		t.Fatal("invalid text sent")
	}
	if err := a.SendTo(context.Background(), "-100", messaging.OutboundMessage{Text: strings.Repeat("😀", 4096), ReplyToMessageID: "42"}); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 1 || !strings.Contains(payloads[0], `"allow_sending_without_reply":false`) || strings.Contains(payloads[0], "parse_mode") {
		t.Fatal(methods, payloads)
	}
	if err := a.RecallMessage(context.Background(), "-100", "987654321"); err != nil || len(methods) != 2 || !strings.HasSuffix(methods[1], "/deleteMessage") || !strings.Contains(payloads[1], `"message_id":987654321`) {
		t.Fatal(err, methods, payloads)
	}
	if err := a.RecallMessage(context.Background(), "-101", "42"); operation.Describe(err).Code != operation.NotConfigured {
		t.Fatal(err)
	}
	for _, id := range []string{"0", "-1", "42x", "042"} {
		if err := a.RecallMessage(context.Background(), "-100", id); operation.Describe(err).Code != operation.Invalid {
			t.Fatal(err)
		}
	}
	a.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		methods = append(methods, "failure")
		return response(400, `{"ok":false,"error_code":400}`), nil
	})
	if err := a.SendTo(context.Background(), "-100", messaging.OutboundMessage{Text: "reply", ReplyToMessageID: "42"}); err == nil || len(methods) != 3 {
		t.Fatal(err, methods)
	}
	select {
	case err := <-a.Errors():
		t.Fatal("operation error became lifecycle failure", err)
	default:
	}
}
