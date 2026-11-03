package decision

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/operation"
)

type providerFunc func(context.Context, ProviderRequest) (Answer, error)

func (f providerFunc) Decide(ctx context.Context, r ProviderRequest) (Answer, error) {
	return f(ctx, r)
}
func sampleTask() Task {
	return Task{ID: "review", Version: 1, Questions: map[string]Choice{"action": {Instructions: "判断是否满足规则，不确定选择unknown", Options: map[string]string{"allow": "满足规则", "deny": "不满足规则", Unknown: "不确定"}}}}
}
func sampleRequest() Request {
	return Request{TaskID: "review", Version: 1, State: []byte(`{"text":"hello"}`)}
}

func TestDMFailureFallsBackOnceAndCachesOnlyWithinRun(t *testing.T) {
	primary, fallback := 0, 0
	s, err := New([]Task{sampleTask()}, Options{Primary: providerFunc(func(context.Context, ProviderRequest) (Answer, error) { primary++; return nil, errors.New("offline") }), Fallback: providerFunc(func(context.Context, ProviderRequest) (Answer, error) {
		fallback++
		return Answer{"action": "deny"}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithBudget(context.Background(), 2)
	result, err := s.Decide(ctx, sampleRequest())
	if err != nil || result.Source != "llm" || result.Answer["action"] != "deny" || len(result.Attempts) != 2 {
		t.Fatalf("%+v %v", result, err)
	}
	result.Answer["action"] = "allow"
	cached, err := s.Decide(ctx, sampleRequest())
	if err != nil || cached.Answer["action"] != "deny" || primary != 1 || fallback != 1 {
		t.Fatalf("cache %+v %v", cached, err)
	}
	if _, err := s.Decide(context.Background(), sampleRequest()); err != nil || primary != 2 {
		t.Fatal("cache leaked across runs", err)
	}
}

func TestUnknownDoesNotRetryAndMalformedResultDoes(t *testing.T) {
	for _, validUnknown := range []bool{true, false} {
		fallback := 0
		answer := Answer{"action": Unknown}
		if !validUnknown {
			answer = Answer{"action": "unexpected"}
		}
		s, err := New([]Task{sampleTask()}, Options{Primary: providerFunc(func(context.Context, ProviderRequest) (Answer, error) { return answer, nil }), Fallback: providerFunc(func(context.Context, ProviderRequest) (Answer, error) {
			fallback++
			return Answer{"action": "deny"}, nil
		})})
		if err != nil {
			t.Fatal(err)
		}
		result, err := s.Decide(context.Background(), sampleRequest())
		if err != nil {
			t.Fatal(err)
		}
		if validUnknown && (fallback != 0 || result.Answer["action"] != Unknown) {
			t.Fatal("unknown retried", result)
		}
		if !validUnknown && (fallback != 1 || result.Answer["action"] != "deny") {
			t.Fatal("invalid decision accepted", result)
		}
	}
}

func TestNoProviderAndBudgetExhaustionRemainUnknown(t *testing.T) {
	s, err := New([]Task{sampleTask()}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Decide(context.Background(), sampleRequest())
	if err != nil || result.Answer["action"] != Unknown || result.Reason != operation.NotConfigured {
		t.Fatalf("%+v %v", result, err)
	}
	calls := 0
	s, err = New([]Task{sampleTask()}, Options{Fallback: providerFunc(func(context.Context, ProviderRequest) (Answer, error) { calls++; return Answer{"action": "allow"}, nil })})
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.Decide(WithBudget(context.Background(), 0), sampleRequest())
	if err != nil || calls != 0 || result.Reason != operation.BudgetExhausted || result.Answer["action"] != Unknown {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestTechnicalTimeoutFallsBackButParentCancellationStops(t *testing.T) {
	fallback := 0
	s, err := New([]Task{sampleTask()}, Options{PrimaryTimeout: time.Millisecond * 5, Primary: providerFunc(func(ctx context.Context, _ ProviderRequest) (Answer, error) { <-ctx.Done(); return nil, ctx.Err() }), Fallback: providerFunc(func(context.Context, ProviderRequest) (Answer, error) {
		fallback++
		return Answer{"action": "deny"}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := s.Decide(context.Background(), sampleRequest()); err != nil || r.Source != "llm" || fallback != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Decide(ctx, sampleRequest()); !errors.Is(err, context.Canceled) || fallback != 1 {
		t.Fatal("canceled request continued", err)
	}
}

type decisionModel func(llm.Request) (llm.Response, error)

func (decisionModel) Protocol() llm.Protocol                                            { return llm.OpenAIResponses }
func (f decisionModel) Complete(_ context.Context, r llm.Request) (llm.Response, error) { return f(r) }
func (decisionModel) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

func TestLLMAdapterIsIsolatedAndStrict(t *testing.T) {
	for _, output := range []string{`{"action":"allow"}`, `{"action":"wrong"}`, `{"action":"allow","extra":true}`, "```json\n{\"action\":\"allow\"}\n```", `{"action":"allow"} {"action":"deny"}`} {
		p := LLMProvider{Model: "answer-model", Client: decisionModel(func(r llm.Request) (llm.Response, error) {
			if len(r.Tools) != 0 || len(r.Messages) != 1 || r.MaxTokens != 512 {
				t.Fatal("decision inherited chat context or tools", r)
			}
			return llm.Response{Text: output}, nil
		})}
		_, err := p.Decide(context.Background(), ProviderRequest{Task: sampleTask(), State: sampleRequest().State})
		if (err == nil) != (output == `{"action":"allow"}`) {
			t.Fatalf("output %q err=%v", output, err)
		}
	}
}
