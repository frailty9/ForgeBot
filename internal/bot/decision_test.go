package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/tooling"
)

type fixedDecider decision.Answer

func (f fixedDecider) Decide(context.Context, decision.Request) (decision.Result, error) {
	return decision.Result{Answer: decision.Answer(f), Source: "rules"}, nil
}

func TestScopeGateAppliesToExplicitMentionWithoutAnswerOrTools(t *testing.T) {
	for _, scope := range []string{"outside", decision.Unknown, "daily"} {
		model := &fakeLLM{response: llm.Response{Text: "must not answer"}}
		tool := &countingTool{}
		policy := testPolicy()
		policy.Scope = "配置文档支持"
		policy.RefusalText = "范围外"
		policy.ClarificationText = "请补充范围"
		sender := newRecordedSender()
		engine, err := newTestEngine(context.Background(), Dependencies{Policy: policy, Sender: sender, LLM: model, Tools: tooling.NewRegistry(tool), Decisions: fixedDecider{"scope": scope, "respond": "yes", "evidence": "required"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := engine.HandleInbound(context.Background(), testInbound("一个简单但域外的专业问题")); err != nil || !result.Accepted {
			t.Fatalf("%+v %v", result, err)
		}
		if len(model.requests) != 0 || tool.count.Load() != 0 || len(sender.Sent()) != 1 {
			t.Fatal("scope refusal invoked answer or tools")
		}
		want := "范围外"
		if scope == decision.Unknown {
			want = "请补充范围"
		}
		if sender.Sent()[0].Text != want {
			t.Fatal(sender.Sent())
		}
		engine.Shutdown(0)
	}
}

func TestDailyAndMixedRequestsKeepRestrictions(t *testing.T) {
	for _, scope := range []string{"daily", "mixed", "in_domain"} {
		model := &fakeLLM{response: llm.Response{Text: "allowed answer"}}
		policy := testPolicy()
		policy.Scope = "部署业务"
		policy.AllowDaily = true
		engine, err := newTestEngine(context.Background(), Dependencies{Sender: newRecordedSender(), LLM: model, Policy: policy, Decisions: fixedDecider{"scope": scope, "respond": "yes", "evidence": "none"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = engine.HandleInbound(context.Background(), testInbound("question")); err != nil {
			t.Fatal(err)
		}
		if len(model.requests) != 1 {
			t.Fatal("allowed request did not reach model")
		}
		if scope == "mixed" && !strings.Contains(model.request.Instructions, "其余部分明确拒答") {
			t.Fatal("mixed request lost restriction")
		}
		engine.Shutdown(0)
	}
}

func TestProactiveUnknownStaysSilentAndImagePolicySkipsDecisions(t *testing.T) {
	policy := testPolicy()
	policy.Scope = "业务"
	sender := newRecordedSender()
	model := &fakeLLM{response: llm.Response{Text: "unexpected"}}
	engine, err := NewEngine(context.Background(), Dependencies{Sender: sender, LLM: model, Policy: policy, Decisions: fixedDecider{"scope": "in_domain", "respond": decision.Unknown}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(0)
	engine.respondDeferred(context.Background(), testInbound("maybe question"))
	if len(model.requests) != 0 || len(sender.Sent()) != 0 {
		t.Fatal("unknown proactive decision spoke")
	}
}

func TestDecisionEvidenceUsesNormalHooksAndToolBudget(t *testing.T) {
	engine, model, retriever, sender := searchEngineFixture(t, []llm.Response{{Text: "supported answer"}})
	defer engine.Shutdown(0)
	engine.deps.Decisions = fixedDecider{"scope": "in_domain", "respond": "yes", "evidence": "required"}
	engine.deps.Policy.EvidenceTool = "search_docs"
	engine.deps.Policy.EvidenceQueryArgument = "query"
	if _, err := engine.HandleInbound(context.Background(), testInbound("authentication")); err != nil {
		t.Fatal(err)
	}
	if retriever.calls != 1 || len(model.requests) != 1 || len(model.request.Messages) != 3 || len(model.request.Tools) != 0 {
		t.Fatalf("calls=%d requests=%+v", retriever.calls, model.requests)
	}
	if sent := sender.Sent(); len(sent) != 2 || sent[0].Text != "正在检索相关文档，请稍候。" {
		t.Fatal(sent)
	}
}
