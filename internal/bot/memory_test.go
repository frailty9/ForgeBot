package bot

import (
	"context"
	"errors"
	"testing"

	"github.com/soleapi/forgebot/internal/conversation"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
)

func TestContextOnlyCommitsConfirmedFinalDelivery(t *testing.T) {
	for _, fail := range []bool{true, false} {
		memory := conversation.NewMemory(conversation.MemoryPolicy{})
		fake := &fakeLLM{response: llm.Response{Text: "answer"}}
		sends := 0
		sender := testSender(func(context.Context, messaging.OutboundMessage) error {
			sends++
			if fail && sends == 1 {
				return errors.New("unknown delivery")
			}
			return nil
		})
		chain, err := hooks.Compile([]hooks.Mount{{ModuleID: "format", Source: hooks.Native, Registrations: hooks.Registrations{BeforeReply: []hooks.Registration[hooks.ReplyInput, hooks.ReplyResult]{{ID: "format", Handle: func(context.Context, hooks.Info, hooks.ReplyInput) (hooks.ReplyResult, error) {
			text := "actual sent answer"
			return hooks.ReplyResult{Text: &text}, nil
		}}}}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		engine, err := NewEngine(context.Background(), Dependencies{Sender: sender, LLM: fake, Memory: memory, Policy: testPolicy(), Hooks: chain}, nil)
		if err != nil {
			t.Fatal(err)
		}
		first := testInbound("first")
		_, err = engine.HandleInbound(context.Background(), first)
		if (err != nil) != fail {
			t.Fatal(err)
		}
		second := testInbound("followup")
		second.MessageID = "second"
		if _, err = engine.HandleInbound(context.Background(), second); err != nil {
			t.Fatal(err)
		}
		want := 3
		if fail {
			want = 1
		}
		if len(fake.request.Messages) != want {
			t.Fatalf("fail=%t messages=%+v", fail, fake.request.Messages)
		}
		if !fail && fake.request.Messages[1].Text() != "actual sent answer" {
			t.Fatal("stored pre-format answer")
		}
		unaddressed := testInbound("still not addressed")
		unaddressed.MessageID = "third"
		unaddressed.Mentioned = false
		if result, err := engine.HandleInbound(context.Background(), unaddressed); err != nil || result.Verdict != "ignored" {
			t.Fatalf("history broadened admission: %+v %v", result, err)
		}
		engine.Shutdown(0)
	}
}

func TestProactiveReplyDoesNotWritePrivateContext(t *testing.T) {
	memory := conversation.NewMemory(conversation.MemoryPolicy{})
	engine, err := NewEngine(context.Background(), Dependencies{Sender: newRecordedSender(), LLM: &fakeLLM{response: llm.Response{Text: "proactive"}}, Memory: memory, Policy: testPolicy()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(0)
	engine.respondDeferred(context.Background(), testInbound("mixed group question"))
	lease, err := memory.Acquire(context.Background(), "c", "u")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if len(lease.History()) != 0 {
		t.Fatal("proactive reply polluted private history")
	}
}
