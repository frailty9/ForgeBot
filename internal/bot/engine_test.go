package bot

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/memory"
)

func TestEngineRepliesToAddressedMessage(t *testing.T) {
	policy := testPolicy()
	policy.Gate.Enabled = true
	registry := platform.NewRegistry()
	sender := memory.NewSender(slog.Default())
	if err := registry.RegisterConnector("echo", sender); err != nil {
		t.Fatal(err)
	}
	if err := registry.BindConversation(platform.ConversationBinding{ConversationID: "c", ConnectorID: "echo", Target: "c"}); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(context.Background(), Dependencies{Sender: registry, Policy: policy}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.HandleInbound(context.Background(), messaging.InboundMessage{
		ConversationID: "c",
		UserID:         "u",
		MessageID:      "m",
		Text:           "hello",
		Mentioned:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || result.Verdict != "pass" {
		t.Fatalf("result = %+v", result)
	}
	if len(sender.Sent()) != 1 {
		t.Fatalf("sent count = %d", len(sender.Sent()))
	}
}

func TestEngineAbsorbsUnaddressedGroupMessage(t *testing.T) {
	policy := testPolicy()
	policy.Gate.Enabled = true
	policy.Gate.RequireMention = false
	policy.Gate.Silence = time.Hour
	registry := platform.NewRegistry()
	sender := memory.NewSender(slog.Default())
	if err := registry.RegisterConnector("echo", sender); err != nil {
		t.Fatal(err)
	}
	if err := registry.BindConversation(platform.ConversationBinding{ConversationID: "c", ConnectorID: "echo", Target: "c"}); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(context.Background(), Dependencies{Sender: registry, Policy: policy}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.HandleInbound(context.Background(), messaging.InboundMessage{
		ConversationID: "c",
		UserID:         "u",
		MessageID:      "m",
		Text:           "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || result.Verdict != "absorbed" {
		t.Fatalf("result = %+v", result)
	}
	if len(sender.Sent()) != 0 {
		t.Fatalf("unexpected sends: %#v", sender.Sent())
	}
}

func TestEngineUsesInjectedLLM(t *testing.T) {
	registry := platform.NewRegistry()
	sender := memory.NewSender(slog.Default())
	if err := registry.RegisterConnector("echo", sender); err != nil {
		t.Fatal(err)
	}
	if err := registry.BindConversation(platform.ConversationBinding{ConversationID: "c", ConnectorID: "echo", Target: "c"}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLLM{response: llm.Response{Text: "model reply"}}
	policy := testPolicy()
	policy.Model = "test-model"
	policy.SystemPrompt = "test-system"
	engine, err := NewEngine(context.Background(), Dependencies{Sender: registry, LLM: fake, Policy: policy}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.HandleInbound(context.Background(), messaging.InboundMessage{
		ConversationID: "c", UserID: "u", MessageID: "m", Text: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.request.Model != "test-model" || fake.request.Instructions != "test-system" || fake.request.Messages[0].Text() != "hello" {
		t.Fatalf("request = %+v", fake.request)
	}
	if sent := sender.Sent(); len(sent) != 1 || sent[0].Text != "model reply" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestEngineRequiresSender(t *testing.T) {
	if _, err := NewEngine(context.Background(), Dependencies{Policy: testPolicy()}, slog.Default()); err == nil {
		t.Fatal("missing sender accepted")
	}
}

type fakeLLM struct {
	request  llm.Request
	response llm.Response
	err      error
}

func (f *fakeLLM) Protocol() llm.Protocol { return llm.OpenAIResponses }
func (f *fakeLLM) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	f.request = request
	return f.response, f.err
}
func (f *fakeLLM) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

func testPolicy() Policy {
	return Policy{Name: "ForgeBot", Gate: GatePolicy{
		RequireMention: true, Silence: 4 * time.Second, MaxCount: 8, MaxWindow: 25 * time.Second,
		DedupTTL: time.Minute, DedupMax: 10000, MaxBuffered: 50, MaxTextLen: 500, MaxConcurrent: 4,
	}}
}
