package group

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/decision"
)

type reviewSecrets struct {
	mu    sync.Mutex
	value string
}

func (r *reviewSecrets) Get(context.Context, string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.value, nil
}

func externalConfig(url string) *ExternalReview {
	return &ExternalReview{URL: url, Method: http.MethodPost, ResultExpression: ".data.approved == true"}
}

func TestExternalReviewValidationAndBooleanExpression(t *testing.T) {
	for _, mutate := range []func(*ExternalReview){
		func(r *ExternalReview) { r.URL = "http://example.com" },
		func(r *ExternalReview) { r.URL = "https://user:pass@example.com" },
		func(r *ExternalReview) { r.Method = "" },
		func(r *ExternalReview) { r.ResultExpression = ".data | true" },
		func(r *ExternalReview) { r.Body = "not JSON" },
		func(r *ExternalReview) { r.Headers = map[string]string{"Authorization": "Bearer {broken"} },
		func(r *ExternalReview) { r.Headers = map[string]string{"bad header": "x"} },
		func(r *ExternalReview) { r.Headers = map[string]string{"Content-Length": "1"} },
		func(r *ExternalReview) { r.Headers = map[string]string{"Authorization": "x\r\ny"} },
	} {
		r := externalConfig("https://example.com/review")
		mutate(r)
		if r.Validate() == nil {
			t.Fatalf("invalid external review accepted: %+v", r)
		}
	}
	for _, address := range []string{"https://example.com", "http://127.0.0.1:1234", "http://[::1]:1234", "http://localhost:1234"} {
		if err := externalConfig(address).Validate(); err != nil {
			t.Fatal(address, err)
		}
	}
	for _, tc := range []struct {
		data, expression string
		want             bool
		bad              bool
	}{
		{`{"data":{"approved":true}}`, ".data.approved == true", true, false},
		{`{"data":{"approved":false}}`, ".data.approved == false", true, false},
		{`{"approved":false}`, ".approved", false, false},
		{`{"approved":"true"}`, ".approved == true", false, true},
		{`{}`, ".approved", false, true},
		{`{} {}`, ".approved", false, true},
	} {
		got, err := reviewResult([]byte(tc.data), tc.expression)
		if got != tc.want || (err != nil) != tc.bad {
			t.Fatalf("%s %s: %v %v", tc.data, tc.expression, got, err)
		}
	}
}

func TestExternalReviewSerializesInputAndResolvesRotatedCredentials(t *testing.T) {
	secret := &reviewSecrets{value: "first-secret"}
	var auth []string
	event := joinEvent()
	event.Comment = "quote\"\\\n{user_id}\",\"approved\":true"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		var body map[string]any
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			t.Error(err)
		}
		if body["application"] != event.Comment || body["user"] != event.UserID || body["group"] != event.ConversationID || body["exact"] != json.Number("9007199254740993") || len(body) != 4 {
			t.Errorf("unsafe template result: %+v", body)
		}
		_, _ = io.WriteString(w, `{"data":{"approved":true}}`)
	}))
	defer server.Close()
	config := externalConfig(server.URL)
	config.Headers = map[string]string{"Authorization": "Bearer {reviews/token}"}
	config.Body = `{"application":"{application}","user":"{user_id}","group":"{conversation_id}","exact":9007199254740993}`
	var logs bytes.Buffer
	s := New(nil, nil, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	s.SetReviewSecrets(secret)
	for _, value := range []string{"first-secret", "second-secret"} {
		secret.mu.Lock()
		secret.value = value
		secret.mu.Unlock()
		approved, err := s.externalReview(context.Background(), *config, event)
		if err != nil || !approved {
			t.Fatal(approved, err)
		}
	}
	if len(auth) != 2 || auth[0] != "Bearer first-secret" || auth[1] != "Bearer second-secret" {
		t.Fatal(auth)
	}
	if strings.Contains(logs.String(), "secret") {
		t.Fatal("credential leaked")
	}
	secret.mu.Lock()
	secret.value = ""
	secret.mu.Unlock()
	if _, err := s.externalReview(context.Background(), *config, event); err == nil || len(auth) != 2 {
		t.Fatal("missing credential sent a request")
	}
}

func TestExternalReviewCombinationShortCircuitsAndTreatsFailuresAsUndecided(t *testing.T) {
	for _, tc := range []struct {
		name, combine, regex, body, model string
		status                            int
		want                              bool
		httpCalls, modelCalls             int
		wantOrder                         string
		undecided                         bool
	}{
		{"only external", "and", "", `{"data":{"approved":true}}`, "", 200, true, 1, 0, "http", false},
		{"and all true", "and", "allowed", `{"data":{"approved":true}}`, "yes", 200, true, 1, 1, "http,model", false},
		{"and external false", "and", "allowed", `{"data":{"approved":false}}`, "yes", 200, false, 1, 0, "http", false},
		{"and regex false", "and", "never", `{"data":{"approved":true}}`, "yes", 200, false, 0, 0, "", false},
		{"and unknown", "and", "allowed", "", "yes", 503, false, 1, 0, "http", true},
		{"and invalid result", "and", "", `{"data":{}}`, "", 200, false, 1, 0, "http", true},
		{"only external unknown", "and", "", "", "", 503, false, 1, 0, "http", true},
		{"or regex true", "or", "allowed", "", "yes", 503, true, 0, 0, "", false},
		{"or external true", "or", "never", `{"data":{"approved":true}}`, "yes", 200, true, 1, 0, "http", false},
		{"or external false model yes", "or", "never", `{"data":{"approved":false}}`, "yes", 200, true, 1, 1, "http,model", false},
		{"or external false model no", "or", "never", `{"data":{"approved":false}}`, "no", 200, false, 1, 1, "http,model", false},
		{"or unknown model yes", "or", "never", "", "yes", 503, true, 1, 1, "http,model", false},
		{"or invalid JSON model yes", "or", "", "not-json", "yes", 200, true, 1, 1, "http,model", false},
		{"or non-boolean model yes", "or", "", `{"data":{"approved":"yes"}}`, "yes", 200, true, 1, 1, "http,model", false},
		{"or unknown model no", "or", "", "", "no", 503, false, 1, 1, "http,model", true},
		{"or unknown no model", "or", "never", "", "", 503, false, 1, 0, "http", true},
		{"and model error", "and", "", `{"data":{"approved":true}}`, "error", 200, false, 1, 1, "http,model", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpCalls, modelCalls := 0, 0
			var mu sync.Mutex
			var order []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				order = append(order, "http")
				mu.Unlock()
				httpCalls++
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			policy := Policy{ExternalReview: externalConfig(server.URL), ReviewCombine: tc.combine, ReviewRegex: tc.regex}
			if tc.model != "" {
				policy.ReviewModelEnabled = boolValue(true)
				policy.ReviewRule = "rule"
			}
			decider := reviewDecider(func(context.Context, decision.Request) (decision.Result, error) {
				mu.Lock()
				order = append(order, "model")
				mu.Unlock()
				modelCalls++
				if tc.model == "error" {
					return decision.Result{}, errors.New("offline")
				}
				return decision.Result{Answer: decision.Answer{"matches": tc.model}}, nil
			})
			actions := &reviewActions{}
			s := New(actions, func(string) ReviewTarget {
				return ReviewTarget{Bound: true, Enabled: true, Policy: policy, Decider: decider}
			})
			result, err := s.HandleEvent(context.Background(), joinEvent())
			mu.Lock()
			gotOrder := strings.Join(order, ",")
			mu.Unlock()
			if gotOrder != tc.wantOrder {
				t.Fatalf("evaluation order = %q, want %q", gotOrder, tc.wantOrder)
			}
			if err != nil || httpCalls != tc.httpCalls || modelCalls != tc.modelCalls {
				t.Fatalf("result=%+v err=%v http=%d model=%d", result, err, httpCalls, modelCalls)
			}
			// 服务或模型故障不得落成拒绝：未决时不执行任何平台审批。
			if tc.undecided {
				if result.Verdict != "undecided" || len(actions.requests) != 0 {
					t.Fatalf("failure became a decision: result=%+v requests=%+v", result, actions.requests)
				}
				return
			}
			if len(actions.requests) != 1 || actions.requests[0].Approve != tc.want {
				t.Fatalf("result=%+v requests=%+v", result, actions.requests)
			}
		})
	}
}

func TestExternalReviewRejectsOversizedResponseAndRedirectsAndCancels(t *testing.T) {
	for _, mode := range []string{"oversized", "redirect", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			arrived := make(chan struct{})
			redirected := 0
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				redirected++
				_, _ = io.WriteString(w, `{"data":{"approved":true}}`)
			}))
			defer destination.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "oversized":
					_, _ = io.WriteString(w, `{"data":{"approved":true}}`+strings.Repeat(" ", 65536))
				case "redirect":
					http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
				case "cancel":
					close(arrived)
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				go func() { <-arrived; cancel() }()
			}
			s := New(nil, nil)
			approved, err := s.externalReview(ctx, *externalConfig(server.URL), joinEvent())
			if err == nil || approved || redirected != 0 {
				t.Fatal(approved, err, redirected)
			}
		})
	}
}

func TestExternalReviewPolicyChangePreventsApproval(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, `{"data":{"approved":true}}`)
	}))
	defer server.Close()
	first := Policy{ExternalReview: externalConfig(server.URL)}
	second := first
	copy := *first.ExternalReview
	copy.Headers = map[string]string{"X-Policy": "updated"}
	second.ExternalReview = &copy
	var mu sync.Mutex
	current := first
	actions := &reviewActions{}
	s := New(actions, func(string) ReviewTarget {
		mu.Lock()
		defer mu.Unlock()
		return ReviewTarget{Bound: true, Enabled: true, Policy: current}
	})
	done := make(chan EventResult, 1)
	go func() { result, _ := s.HandleEvent(context.Background(), joinEvent()); done <- result }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("external request did not start")
	}
	mu.Lock()
	current = second
	mu.Unlock()
	close(release)
	if result := <-done; result.Verdict != "policy_changed" || len(actions.requests) != 0 {
		t.Fatal(result, actions.requests)
	}
	clone := first
	clonedConfig := *first.ExternalReview
	clone.ExternalReview = &clonedConfig
	if !clone.Equal(first) || second.Equal(first) {
		t.Fatal("policy equality used pointer identity or ignored headers")
	}
}
