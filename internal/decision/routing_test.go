package decision

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/operation"
)

func TestDecisionSelectsOneModelWithoutRetry(t *testing.T) {
	for _, mode := range []string{"success", "error", "malformed", "unknown", "timeout", "unsupported", "missing-key", "llm-only", "llm-error", "unconfigured"} {
		t.Run(mode, func(t *testing.T) {
			dmCalls, llmCalls := 0, 0
			opts := Options{DMTimeout: 5 * time.Millisecond,
				DM: providerFunc(func(ctx context.Context, _ ProviderRequest) (Answer, error) {
					dmCalls++
					switch mode {
					case "error":
						return nil, errors.New("offline")
					case "malformed":
						return Answer{"action": "invalid"}, nil
					case "unknown":
						return Answer{"action": Unknown}, nil
					case "timeout":
						<-ctx.Done()
						return nil, ctx.Err()
					case "unsupported":
						return nil, operation.New(operation.Unsupported, "input unsupported")
					case "missing-key":
						return nil, operation.New(operation.NotConfigured, "missing credential")
					default:
						return Answer{"action": "deny"}, nil
					}
				}),
				LLM: providerFunc(func(context.Context, ProviderRequest) (Answer, error) {
					llmCalls++
					if mode == "llm-error" {
						return nil, errors.New("offline")
					}
					return Answer{"action": "allow"}, nil
				}),
			}
			if strings.HasPrefix(mode, "llm-") {
				opts.DM = nil
			}
			if mode == "unconfigured" {
				opts.DM, opts.LLM = nil, nil
			}
			s, err := New([]Task{sampleTask()}, opts)
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.Decide(context.Background(), sampleRequest())
			wantErr := mode != "success" && mode != "unknown" && mode != "llm-only"
			if (err != nil) != wantErr {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			wantDM, wantLLM := 1, 0
			if strings.HasPrefix(mode, "llm-") {
				wantDM, wantLLM = 0, 1
			}
			if mode == "unconfigured" {
				wantDM = 0
			}
			if dmCalls != wantDM || llmCalls != wantLLM || len(result.Attempts) != wantDM+wantLLM {
				t.Fatalf("dm=%d llm=%d attempts=%+v", dmCalls, llmCalls, result.Attempts)
			}
			if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}

func TestDecisionCacheBudgetAndCancellation(t *testing.T) {
	calls := 0
	fail := false
	s, err := New([]Task{sampleTask()}, Options{LLM: providerFunc(func(context.Context, ProviderRequest) (Answer, error) {
		calls++
		if fail {
			return nil, errors.New("offline")
		}
		return Answer{"action": "deny"}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithBudget(context.Background(), 1)
	result, err := s.Decide(ctx, sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	result.Answer["action"] = "allow"
	cached, err := s.Decide(ctx, sampleRequest())
	if err != nil || calls != 1 || cached.Answer["action"] != "deny" {
		t.Fatal(cached, calls, err)
	}
	other := sampleRequest()
	other.State = json.RawMessage(`{"text":"other"}`)
	if _, err = s.Decide(ctx, other); operation.Describe(err).Code != operation.BudgetExhausted || calls != 1 {
		t.Fatal(calls, err)
	}
	if _, err = s.Decide(context.Background(), sampleRequest()); err != nil || calls != 2 {
		t.Fatal(calls, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Decide(canceled, sampleRequest()); !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatal(calls, err)
	}
	fail = true
	ctx = WithBudget(context.Background(), 2)
	for i := 0; i < 2; i++ {
		if _, err = s.Decide(ctx, sampleRequest()); err == nil {
			t.Fatal("failure cached as success")
		}
	}
	if calls != 4 {
		t.Fatalf("failed results cached: calls=%d", calls)
	}
}

func TestDecideBoolDistinguishesFalseUnknownAndFailure(t *testing.T) {
	for _, outcome := range []string{"yes", "no", Unknown, "invalid", "error"} {
		t.Run(outcome, func(t *testing.T) {
			calls := 0
			s, err := New(nil, Options{LLM: providerFunc(func(_ context.Context, r ProviderRequest) (Answer, error) {
				calls++
				var state struct {
					Rule string
					Data json.RawMessage
				}
				if err := json.Unmarshal(r.State, &state); err != nil || state.Rule != "rule" || string(state.Data) != `{"input":"data"}` {
					t.Fatal(string(r.State), err)
				}
				if outcome == "error" {
					return nil, errors.New("offline")
				}
				return Answer{"matches": outcome}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.DecideBool(context.Background(), "rule", json.RawMessage(`{"input":"data"}`))
			if result != (outcome == "yes") || (err != nil) != (outcome != "yes" && outcome != "no") || calls != 1 {
				t.Fatal(result, calls, err)
			}
			if outcome == Unknown && !errors.Is(err, ErrUndecided) {
				t.Fatal(err)
			}
		})
	}
}

func TestBooleanRulesParticipateInCacheKeyAndValidateBeforeCalling(t *testing.T) {
	calls := 0
	s, err := New(nil, Options{LLM: providerFunc(func(context.Context, ProviderRequest) (Answer, error) { calls++; return Answer{"matches": "no"}, nil })})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithBudget(context.Background(), 2)
	for _, rule := range []string{"first", "first", "second"} {
		if value, err := s.DecideBool(ctx, rule, json.RawMessage(`{}`)); err != nil || value {
			t.Fatal(value, err)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	for _, input := range []struct{ rule, data string }{{"", `{}`}, {strings.Repeat("x", 4097), `{}`}, {"rule", `broken`}, {"rule", `"` + strings.Repeat("x", 16384) + `"`}} {
		if _, err := s.DecideBool(ctx, input.rule, json.RawMessage(input.data)); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if calls != 2 {
		t.Fatal("invalid input reached model")
	}
}
