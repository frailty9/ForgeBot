package decision

import (
	"context"
	"testing"

	"github.com/soleapi/forgebot/internal/llm"
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

type decisionModel func(llm.Request) (llm.Response, error)

func (decisionModel) Protocol() llm.Protocol                                            { return llm.OpenAIResponses }
func (f decisionModel) Complete(_ context.Context, r llm.Request) (llm.Response, error) { return f(r) }
func (decisionModel) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

func TestLLMAdapterIsIsolatedAndStrict(t *testing.T) {
	for _, output := range []string{`{"action":"allow"}`, `{"action":"wrong"}`, `{"action":"allow","extra":true}`, "```json\n{\"action\":\"allow\"}\n```", `{"action":"allow"} {"action":"deny"}`, `{"action":"deny","action":"allow"}`} {
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
