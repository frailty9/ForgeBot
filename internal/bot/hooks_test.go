package bot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/gate"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/replyexperience"
	"github.com/soleapi/forgebot/internal/platform/memory"
	"github.com/soleapi/forgebot/internal/tooling"
)

type testSender func(context.Context, messaging.OutboundMessage) error

type recordedSender struct{ *memory.Sender }

func newRecordedSender() *recordedSender { return &recordedSender{memory.NewSender(nil)} }
func (s *recordedSender) Send(ctx context.Context, msg messaging.OutboundMessage) error {
	return s.SendTo(ctx, msg.ConversationID, msg)
}

func (s testSender) Send(ctx context.Context, message messaging.OutboundMessage) error {
	return s(ctx, message)
}

type testClient func(context.Context, llm.Request) (llm.Response, error)

func (testClient) Protocol() llm.Protocol { return llm.AnthropicMessages }
func (c testClient) Complete(ctx context.Context, request llm.Request) (llm.Response, error) {
	return c(ctx, request)
}
func (testClient) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

type countingTool struct{ count atomic.Int32 }

func (*countingTool) Definition() tooling.Definition {
	return tooling.Definition{Name: "search_docs", InputSchema: map[string]any{"type": "object"}}
}
func (tool *countingTool) Execute(context.Context, tooling.Call) tooling.Result {
	tool.count.Add(1)
	return tooling.Result{OK: true, Payload: "found"}
}

func testHooks(t *testing.T, enabled bool) (*hooks.Chain, func(context.Context) error) {
	t.Helper()
	registry := modules.NewRegistry()
	if err := registry.Register(replyexperience.Factory{}, hooks.Native, 100, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	set, err := registry.Build(context.Background(), map[string]config.ModuleConfig{replyexperience.ID: {Enabled: &enabled}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return set.Chain(), set.Close
}

func TestModuleDisabledPreservesMediaAndIgnoresEmptyMention(t *testing.T) {
	chain, closeModules := testHooks(t, false)
	sender := newRecordedSender()
	fake := &fakeLLM{response: llm.Response{Text: "image reply"}}
	engine, err := NewEngine(context.Background(), Dependencies{Sender: sender, LLM: fake, Policy: testPolicy(), Hooks: chain, CloseHooks: closeModules}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(0)
	msg := testInbound("")
	msg.Mentioned = true
	result, err := engine.HandleInbound(context.Background(), msg)
	if err != nil || result.Verdict != "empty_input" || len(fake.requests) != 0 || len(sender.Sent()) != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	msg.Media = []messaging.Media{{Kind: messaging.MediaImage, URL: "https://example.test/image.png"}}
	msg.MessageID = "image"
	result, err = engine.HandleInbound(context.Background(), msg)
	if err != nil || !result.Accepted {
		t.Fatal(result, err)
	}
	parts := fake.request.Messages[0].Content
	if len(parts) != 1 || parts[0].Type != "image" {
		t.Fatal(parts)
	}
}

func TestAllEngineRepliesUseReplyHook(t *testing.T) {
	var kinds []string
	var infos []hooks.Info
	chain, err := hooks.Compile([]hooks.Mount{{ModuleID: "format", Source: hooks.External, Registrations: hooks.Registrations{BeforeReply: []hooks.Registration[hooks.ReplyInput, hooks.ReplyResult]{{ID: "format", Handle: func(_ context.Context, info hooks.Info, input hooks.ReplyInput) (hooks.ReplyResult, error) {
		kinds = append(kinds, input.Kind)
		infos = append(infos, info)
		text := "formatted:" + input.Text
		return hooks.ReplyResult{Text: &text}, nil
	}}}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sender := newRecordedSender()
	engine, err := NewEngine(context.Background(), Dependencies{Sender: sender, Policy: testPolicy(), Hooks: chain, Generation: 42}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(0)
	if _, err := engine.HandleInbound(context.Background(), testInbound("hi")); err != nil {
		t.Fatal(err)
	}
	engine.onGateFlush(context.Background(), gate.Batch{ConversationID: "c", Msgs: []gate.Message{{MsgID: "p", UserID: "u", Text: "question"}}})
	ctx := engine.beginRun(context.Background(), testInbound("hi"), "direct")
	if err := engine.deliver(ctx, testInbound("hi"), "waiting", "progress"); err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 3 || kinds[2] != "progress" || infos[0].RunID == infos[1].RunID || infos[1].Trigger != "proactive" || infos[0].Generation != 42 {
		t.Fatal(kinds, infos)
	}
	for _, sent := range sender.Sent() {
		if !strings.HasPrefix(sent.Text, "formatted:") {
			t.Fatal(sent)
		}
	}
}

func TestReplySuppressionIsNotDeliverySuccess(t *testing.T) {
	chain, err := hooks.Compile([]hooks.Mount{{ModuleID: "suppress", Source: hooks.External, Registrations: hooks.Registrations{BeforeReply: []hooks.Registration[hooks.ReplyInput, hooks.ReplyResult]{{ID: "stop", Handle: func(context.Context, hooks.Info, hooks.ReplyInput) (hooks.ReplyResult, error) {
		return hooks.ReplyResult{Suppress: true}, nil
	}}}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sender := newRecordedSender()
	engine, err := NewEngine(context.Background(), Dependencies{Sender: sender, Policy: testPolicy(), Hooks: chain}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(0)
	result, err := engine.HandleInbound(context.Background(), testInbound("hi"))
	if err != nil || result.Accepted || result.Verdict != "reply_suppressed" || len(sender.Sent()) != 0 {
		t.Fatal(result, err)
	}
}

func TestProgressFailureOncePerRunAndConcurrentIsolation(t *testing.T) {
	chain, closeModules := testHooks(t, true)
	var progress, final atomic.Int32
	sender := testSender(func(_ context.Context, msg messaging.OutboundMessage) error {
		if msg.Text == replyexperience.SearchProgressText {
			progress.Add(1)
			return errors.New("delivery failed")
		}
		final.Add(1)
		return nil
	})
	tool := &countingTool{}
	client := testClient(func(_ context.Context, req llm.Request) (llm.Response, error) {
		if len(req.Messages) == 1 {
			return llm.Response{Message: llm.Message{ToolCalls: []llm.ToolCall{{ID: "one", Name: "search_docs", Arguments: `{}`}, {ID: "two", Name: "search_docs", Arguments: `{}`}}}}, nil
		}
		if req.Messages[2].ToolCallID != "one" || req.Messages[3].ToolCallID != "two" {
			return llm.Response{}, errors.New("lost call id")
		}
		return llm.Response{Text: "done"}, nil
	})
	policy := testPolicy()
	policy.Search = SearchPolicy{Enabled: true, MaxQueriesPerTurn: 2}
	engine, err := NewEngine(context.Background(), Dependencies{Sender: sender, LLM: client, Tools: tooling.NewRegistry(tool), Hooks: chain, CloseHooks: closeModules, Policy: policy}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(0)
	var workers sync.WaitGroup
	for _, group := range []string{"one", "two"} {
		workers.Add(1)
		go func(group string) {
			defer workers.Done()
			msg := testInbound("question")
			msg.ConversationID = group
			result, err := engine.HandleInbound(context.Background(), msg)
			if err != nil || !result.Accepted {
				t.Error(result, err)
			}
		}(group)
	}
	workers.Wait()
	if progress.Load() != 2 || final.Load() != 2 || tool.count.Load() != 4 {
		t.Fatal(progress.Load(), final.Load(), tool.count.Load())
	}
}

func TestToolHooksCannotExpandAllowlistAndDenyBeforeProgress(t *testing.T) {
	var hookCalls, deliveries atomic.Int32
	chain, err := hooks.Compile([]hooks.Mount{{ModuleID: "a", Source: hooks.Native, Registrations: hooks.Registrations{BeforeTool: []hooks.Registration[hooks.ToolInput, hooks.ToolResult]{{ID: "progress", Handle: func(context.Context, hooks.Info, hooks.ToolInput) (hooks.ToolResult, error) {
		hookCalls.Add(1)
		return hooks.ToolResult{Progress: &hooks.ProgressEffect{Text: "wait"}}, nil
	}}}}}, {ModuleID: "b", Source: hooks.External, Registrations: hooks.Registrations{BeforeTool: []hooks.Registration[hooks.ToolInput, hooks.ToolResult]{{ID: "deny", Handle: func(context.Context, hooks.Info, hooks.ToolInput) (hooks.ToolResult, error) {
		return hooks.ToolResult{Deny: true}, nil
	}}}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeLLM{responses: []llm.Response{{Message: llm.Message{ToolCalls: []llm.ToolCall{{ID: "x", Name: "unknown", Arguments: `{}`}, {ID: "y", Name: "search_docs", Arguments: `{}`}, {ID: "z", Name: "search_docs", Arguments: `{}`}}}}, {Text: "final"}}}
	tool := &countingTool{}
	policy := testPolicy()
	policy.Search = SearchPolicy{Enabled: true, MaxQueriesPerTurn: 2}
	engine, err := NewEngine(context.Background(), Dependencies{Sender: testSender(func(context.Context, messaging.OutboundMessage) error { deliveries.Add(1); return nil }), LLM: fake, Tools: tooling.NewRegistry(tool), Policy: policy, Hooks: chain}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(0)
	if _, err := engine.HandleInbound(context.Background(), testInbound("question")); err != nil {
		t.Fatal(err)
	}
	if tool.count.Load() != 0 || hookCalls.Load() != 1 || deliveries.Load() != 1 {
		t.Fatal(tool.count.Load(), hookCalls.Load(), deliveries.Load())
	}
	messages := fake.requests[1].Messages
	for index, code := range []string{"tool_not_allowed", "module_denied", "budget_exhausted"} {
		var result tooling.Result
		if err := json.Unmarshal([]byte(messages[index+2].Text()), &result); err != nil || result.Code != code {
			t.Fatal(result, err)
		}
	}
}

func TestShutdownWaitsForActiveGateBeforeClosingModules(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var closed atomic.Int32
	policy := testPolicy()
	policy.Gate.Enabled = true
	policy.Gate.AutoReplyUnmentioned = true
	policy.Gate.Silence = time.Millisecond
	client := testClient(func(context.Context, llm.Request) (llm.Response, error) {
		close(entered)
		<-release
		return llm.Response{Text: "done"}, nil
	})
	engine, err := NewEngine(context.Background(), Dependencies{Sender: testSender(func(context.Context, messaging.OutboundMessage) error { return nil }), LLM: client, Policy: policy, CloseHooks: func(context.Context) error { closed.Add(1); return nil }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(0)
	defer close(release)
	message := testInbound("question")
	message.Mentioned = false
	if _, err := engine.HandleInbound(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("gate did not flush")
	}
	engine.Shutdown(time.Millisecond)
	if closed.Load() != 0 {
		t.Fatal("modules closed during gate callback")
	}
	select {
	case <-engine.Done():
		t.Fatal("engine closed early")
	default:
	}
}

func TestCanceledRequestDoesNotCallModel(t *testing.T) {
	fake := &fakeLLM{}
	engine, err := NewEngine(context.Background(), Dependencies{Sender: newRecordedSender(), LLM: fake, Policy: testPolicy()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.HandleInbound(ctx, testInbound("hi")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(fake.requests) != 0 {
		t.Fatal("model called after cancel")
	}
}

func TestUnresolvedMediaIsNotAnEmptyMention(t *testing.T) {
	chain, closeModules := testHooks(t, true)
	fake := &fakeLLM{}
	engine, err := NewEngine(context.Background(), Dependencies{Sender: newRecordedSender(), LLM: fake, Policy: testPolicy(), Hooks: chain, CloseHooks: closeModules}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(0)
	message := testInbound("")
	message.Mentioned = true
	message.Media = []messaging.Media{{Kind: messaging.MediaImage, Name: "unresolved-file"}}
	result, err := engine.HandleInbound(context.Background(), message)
	if err != nil || result.Verdict != "empty_input" || len(fake.requests) != 0 {
		t.Fatal(result, err)
	}
}
