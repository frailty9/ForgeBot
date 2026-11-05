package group

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/operation"
)

type reviewDecider func(context.Context, decision.Request) (decision.Result, error)

func (f reviewDecider) Decide(ctx context.Context, request decision.Request) (decision.Result, error) {
	return f(ctx, request)
}

type reviewActions struct {
	mu       sync.Mutex
	requests []ReviewRequest
	reason   bool
	fail     error
}

func (a *reviewActions) Support(context.Context, string) (Support, error) {
	return Support{JoinReview: true, JoinReviewReason: a.reason}, nil
}
func (a *reviewActions) Review(_ context.Context, _ string, request ReviewRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, request)
	return a.fail
}

func boolValue(value bool) *bool { return &value }
func joinEvent() Event {
	return Event{ID: "event", Kind: JoinRequested, ConversationID: "g", ConnectorID: "one", UserID: "u", Reference: "private-handle", Comment: "allowed", At: time.Now()}
}

func TestAutomaticReviewCombinationAndShortCircuit(t *testing.T) {
	for _, tc := range []struct {
		name, regex, combine, answer string
		model                        bool
		verdict                      string
		calls                        int
	}{
		{"regex-yes", "allowed", "and", "", false, "approved", 0},
		{"regex-no", "denied", "and", "", false, "rejected", 0},
		{"llm-yes", "", "and", "yes", true, "approved", 1},
		{"llm-no", "", "and", "no", true, "rejected", 1},
		{"and-short-circuit", "denied", "and", "yes", true, "rejected", 0},
		{"and-yes", "allowed", "and", "yes", true, "approved", 1},
		{"and-no", "allowed", "and", "no", true, "rejected", 1},
		{"and-unknown", "allowed", "and", decision.Unknown, true, "undecided", 1},
		{"or-short-circuit", "allowed", "or", "no", true, "approved", 0},
		{"or-yes", "denied", "or", "yes", true, "approved", 1},
		{"or-no", "denied", "or", "no", true, "rejected", 1},
		{"or-unknown", "denied", "or", decision.Unknown, true, "undecided", 1},
		{"model-error", "", "and", "error", true, "undecided", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			d := reviewDecider(func(_ context.Context, r decision.Request) (decision.Result, error) {
				calls++
				if r.TaskID != decision.BooleanTaskID {
					t.Fatal(r)
				}
				if tc.answer == "error" {
					return decision.Result{}, errors.New("offline")
				}
				return decision.Result{Answer: decision.Answer{"matches": tc.answer}}, nil
			})
			p := Policy{ReviewEnabled: true, AutoReview: true, ReviewRegex: tc.regex, ReviewRule: "must be allowed", ReviewModelEnabled: boolValue(tc.model), ReviewCombine: tc.combine}
			port := &reviewActions{}
			s := New(nil, port, func(string) (Policy, decision.Decider, bool) { return p, d, true })
			result, err := s.HandleEvent(context.Background(), joinEvent())
			if err != nil || result.Verdict != tc.verdict || calls != tc.calls {
				t.Fatalf("%+v calls=%d err=%v", result, calls, err)
			}
			want := 1
			if tc.verdict == "undecided" {
				want = 0
			}
			if len(port.requests) != want {
				t.Fatal(port.requests)
			}
			if want == 1 && (port.requests[0].Approve != (tc.verdict == "approved") || port.requests[0].Reference != "private-handle") {
				t.Fatal(port.requests)
			}
		})
	}
}

func TestReviewRejectReasonAndLogsWithoutPersistence(t *testing.T) {
	for _, supportsReason := range []bool{true, false} {
		var logs bytes.Buffer
		port := &reviewActions{reason: supportsReason}
		p := Policy{ReviewEnabled: true, AutoReview: true, ReviewRegex: `\S`, RejectReason: "请填写申请说明"}
		s := New(nil, port, func(string) (Policy, decision.Decider, bool) { return p, nil, true }, slog.New(slog.NewJSONHandler(&logs, nil)))
		event := joinEvent()
		event.Comment = " \t\n"
		result, err := s.HandleEvent(context.Background(), event)
		if err != nil || result.Verdict != "rejected" || len(port.requests) != 1 || port.requests[0].Approve {
			t.Fatal(result, err, port.requests)
		}
		wantReason := ""
		if supportsReason {
			wantReason = p.RejectReason
		}
		if port.requests[0].Reason != wantReason {
			t.Fatal(port.requests)
		}
		if strings.Contains(logs.String(), event.Reference) || !strings.Contains(logs.String(), `"verdict":"rejected"`) {
			t.Fatal(logs.String())
		}
	}
}

func TestReviewConcurrentDuplicatesAndExecutionFailureNeverReplay(t *testing.T) {
	port := &reviewActions{fail: operation.New(operation.Unavailable, "offline")}
	var calls atomic.Int32
	d := reviewDecider(func(context.Context, decision.Request) (decision.Result, error) {
		calls.Add(1)
		return decision.Result{Answer: decision.Answer{"matches": "yes"}}, nil
	})
	p := Policy{ReviewEnabled: true, AutoReview: true, ReviewRule: "rule"}
	s := New(nil, port, func(string) (Policy, decision.Decider, bool) { return p, d, true })
	event := joinEvent()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.HandleEvent(context.Background(), event) }()
	}
	wg.Wait()
	if calls.Load() != 1 || len(port.requests) != 1 {
		t.Fatal(calls.Load(), len(port.requests))
	}
	if r, err := s.HandleEvent(context.Background(), event); err != nil || r.Verdict != "duplicate" {
		t.Fatal(r, err)
	}
}

func TestReviewPolicyChangeAndEffectiveEquality(t *testing.T) {
	for _, changed := range []bool{true, false} {
		p := Policy{ReviewEnabled: true, AutoReview: true, ReviewRule: "rule", ReviewModelEnabled: boolValue(true)}
		port := &reviewActions{}
		d := reviewDecider(func(context.Context, decision.Request) (decision.Result, error) {
			p.ReviewModelEnabled = boolValue(true)
			if changed {
				p.ReviewRule = "new rule"
			}
			return decision.Result{Answer: decision.Answer{"matches": "yes"}}, nil
		})
		s := New(nil, port, func(string) (Policy, decision.Decider, bool) { return p, d, true })
		result, err := s.HandleEvent(context.Background(), joinEvent())
		if err != nil {
			t.Fatal(err)
		}
		if changed && (len(port.requests) != 0 || result.Verdict != "policy_changed") {
			t.Fatal(result, port.requests)
		}
		if !changed && (len(port.requests) != 1 || result.Verdict != "approved") {
			t.Fatal(result, port.requests)
		}
	}
}

func TestReviewDedupExpiryCapacityAndGroupIsolation(t *testing.T) {
	s := New(nil, nil, nil)
	now := time.Now()
	s.now = func() time.Time { return now }
	event := joinEvent()
	if claimed, err := s.claimReview(event); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	if claimed, err := s.claimReview(event); err != nil || claimed {
		t.Fatal(claimed, err)
	}
	other := event
	other.ConversationID = "other"
	if claimed, err := s.claimReview(other); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	now = now.Add(24 * time.Hour)
	if claimed, err := s.claimReview(event); err != nil || !claimed || len(s.reviews) != 1 {
		t.Fatal(claimed, err, len(s.reviews))
	}
	for i := 0; i < maxReviewEvents; i++ {
		s.reviews[fmt.Sprint(i)] = now.Add(time.Hour)
	}
	if _, err := s.claimReview(other); operation.Describe(err).Code != operation.Busy {
		t.Fatal(err)
	}
}

func TestReviewDisabledExpiredAndCanceledDoNotAct(t *testing.T) {
	for _, mode := range []string{"disabled", "manual-only", "expired", "future", "historical", "canceled", "missing-identity", "missing-model"} {
		t.Run(mode, func(t *testing.T) {
			p := Policy{ReviewEnabled: true, AutoReview: true, ReviewRegex: `\S`}
			event := joinEvent()
			ctx := context.Background()
			switch mode {
			case "disabled":
				p.ReviewEnabled, p.AutoReview = false, false
			case "manual-only":
				p.AutoReview = false
			case "expired":
				event.At = time.Now().Add(-25 * time.Hour)
			case "future":
				event.At = time.Now().Add(time.Hour)
			case "historical":
				event.Historical = true
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "missing-identity":
				event.Reference = ""
			case "missing-model":
				p.ReviewRegex = ""
				p.ReviewRule = "rule"
			}
			port := &reviewActions{}
			s := New(nil, port, func(string) (Policy, decision.Decider, bool) { return p, nil, true })
			_, _ = s.HandleEvent(ctx, event)
			if len(port.requests) != 0 {
				t.Fatal(port.requests)
			}
		})
	}
}

func TestPolicyValidationLegacyDefaultsAndExplicitDisable(t *testing.T) {
	for _, raw := range []string{
		`{"review_enabled":true,"auto_review":true,"review_regex":"["}`,
		`{"review_enabled":true,"auto_review":true}`,
		`{"auto_review":true,"review_regex":"x"}`,
		`{"review_combine":"other"}`,
		`{"review_model_enabled":true}`,
	} {
		var p Policy
		if err := json.Unmarshal([]byte(raw), &p); err == nil && p.Validate() == nil {
			t.Fatal("invalid policy accepted", raw)
		}
	}
	legacy := Policy{ReviewEnabled: true, AutoReview: true, ReviewRule: "rule"}
	if err := legacy.Validate(); err != nil || !legacy.ModelEnabled() || legacy.Combine() != "and" {
		t.Fatal(legacy, err)
	}
	legacy.ReviewModelEnabled = boolValue(false)
	legacy.ReviewRegex = `\S`
	if err := legacy.Validate(); err != nil || legacy.ModelEnabled() {
		t.Fatal(legacy, err)
	}
	legacy.RejectReason = strings.Repeat("x", 2049)
	if legacy.Validate() == nil {
		t.Fatal("oversized reason accepted")
	}
}
