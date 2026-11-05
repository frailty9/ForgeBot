package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/llm"
)

const proactiveHint = "本次是未被 @ 的主动回复"

// 明确 @ 只请求一次回答模型，不附加主动回复提示；范围由部署者的系统提示词约束。
func TestExplicitMentionAnswersOnceWithoutProactiveHint(t *testing.T) {
	model := &fakeLLM{response: llm.Response{Text: "answer"}}
	policy := testPolicy()
	policy.SystemPrompt = "只回答配置文档相关问题"
	sender := newRecordedSender()
	engine, err := newTestEngine(context.Background(), Dependencies{Policy: policy, Sender: sender, LLM: model}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(0)
	if result, err := engine.HandleInbound(context.Background(), testInbound("一个域外专业问题")); err != nil || !result.Accepted {
		t.Fatalf("%+v %v", result, err)
	}
	if len(model.requests) != 1 || !strings.Contains(model.request.Instructions, "配置文档") || strings.Contains(model.request.Instructions, proactiveHint) {
		t.Fatal("unexpected answer instructions", model.request.Instructions)
	}
	if sent := sender.Sent(); len(sent) != 1 || sent[0].Text != "answer" {
		t.Fatal(sent)
	}
}

// 主动回复带宿主提示；回答模型判断不该回复而输出空内容时不发送任何消息。
func TestProactiveReplyCarriesHintAndEmptyOutputStaysSilent(t *testing.T) {
	for _, text := range []string{"", "proactive answer"} {
		model := &fakeLLM{response: llm.Response{Text: text}}
		sender := newRecordedSender()
		engine, err := NewEngine(context.Background(), Dependencies{Sender: sender, LLM: model, Policy: testPolicy()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		engine.respondDeferred(context.Background(), testInbound("maybe question"))
		if len(model.requests) != 1 || !strings.Contains(model.request.Instructions, proactiveHint) {
			t.Fatal("proactive hint missing", model.request.Instructions)
		}
		if sent := sender.Sent(); (text == "" && len(sent) != 0) || (text != "" && len(sent) != 1) {
			t.Fatalf("text=%q sent=%v", text, sent)
		}
		engine.Shutdown(0)
	}
}
