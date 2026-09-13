package bot

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/memory"
	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/tooling"
	"github.com/soleapi/forgebot/internal/tooling/searchdocs"
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
	policy.Gate.AutoReplyUnmentioned = true
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

func TestEngineAppendsPromptSuffix(t *testing.T) {
	registry := platform.NewRegistry()
	sender := memory.NewSender(slog.Default())
	if err := registry.RegisterConnector("echo", sender); err != nil {
		t.Fatal(err)
	}
	if err := registry.BindConversation(platform.ConversationBinding{ConversationID: "c", ConnectorID: "echo", Target: "c"}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLLM{response: llm.Response{Text: "plain", Message: llm.TextMessage(llm.RoleAssistant, "plain")}}
	policy := testPolicy()
	policy.SystemPrompt = "base"
	engine, err := NewEngine(context.Background(), Dependencies{Sender: registry, LLM: fake, Policy: policy}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.HandleInbound(context.Background(), messaging.InboundMessage{PromptSuffix: "仅使用纯文本，不要使用 Markdown。", ConversationID: "c", UserID: "u", MessageID: "m", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if fake.request.Instructions != "base\n\n仅使用纯文本，不要使用 Markdown。" {
		t.Fatalf("instructions = %q", fake.request.Instructions)
	}
}

func TestEngineRequiresSender(t *testing.T) {
	if _, err := NewEngine(context.Background(), Dependencies{Policy: testPolicy()}, slog.Default()); err == nil {
		t.Fatal("missing sender accepted")
	}
}

func TestEngineDoesNotSearchWhenModelAnswersDirectly(t *testing.T) {
	engine, fake, retriever, sender := searchEngineFixture(t, []llm.Response{{Text: "direct", Message: llm.TextMessage(llm.RoleAssistant, "direct")}})
	if _, err := engine.HandleInbound(context.Background(), testInbound("simple question")); err != nil {
		t.Fatal(err)
	}
	if retriever.calls != 0 || len(fake.requests) != 1 || len(fake.requests[0].Tools) != 1 {
		t.Fatalf("retriever calls=%d requests=%+v", retriever.calls, fake.requests)
	}
	if sent := sender.Sent(); len(sent) != 1 || sent[0].Text != "direct" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestEngineExecutesOneSearchToolAndStopsLoop(t *testing.T) {
	call := llm.ToolCall{ID: "call-1", Name: searchdocs.Name, Arguments: `{"query":"authentication"}`}
	engine, fake, retriever, sender := searchEngineFixture(t, []llm.Response{
		{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}},
		{Text: "document answer", Message: llm.TextMessage(llm.RoleAssistant, "document answer")},
	})
	if _, err := engine.HandleInbound(context.Background(), testInbound("how to authenticate?")); err != nil {
		t.Fatal(err)
	}
	if retriever.calls != 1 || len(fake.requests) != 2 || len(fake.requests[1].Tools) != 0 {
		t.Fatalf("retriever calls=%d requests=%+v", retriever.calls, fake.requests)
	}
	messages := fake.requests[1].Messages
	if len(messages) != 3 || messages[1].ToolCalls[0].ID != "call-1" || messages[2].Role != llm.RoleTool || !strings.Contains(messages[2].Text(), "untrusted") && !strings.Contains(messages[2].Text(), "不可信") {
		t.Fatalf("tool messages = %+v", messages)
	}
	sent := sender.Sent()
	if len(sent) != 2 || sent[0].Text != searchPreReply || sent[1].Text != "document answer" {
		t.Fatalf("sent = %+v", sender.Sent())
	}
}

func TestEngineExplicitSearchSkipsDecisionAndHandlesEmptyQuery(t *testing.T) {
	engine, fake, retriever, sender := searchEngineFixture(t, []llm.Response{{Text: "cited answer", Message: llm.TextMessage(llm.RoleAssistant, "cited answer")}})
	if _, err := engine.HandleInbound(context.Background(), testInbound("/search authentication")); err != nil {
		t.Fatal(err)
	}
	if retriever.calls != 1 || len(fake.requests) != 1 || len(fake.requests[0].Tools) != 0 {
		t.Fatalf("retriever calls=%d requests=%+v", retriever.calls, fake.requests)
	}
	if sender.Sent()[0].Text != "cited answer" {
		t.Fatalf("sent = %+v", sender.Sent())
	}
	engine, fake, retriever, sender = searchEngineFixture(t, nil)
	if _, err := engine.HandleInbound(context.Background(), testInbound("/search")); err != nil {
		t.Fatal(err)
	}
	if retriever.calls != 0 || len(fake.requests) != 0 || sender.Sent()[0].Text != "用法：/search <关键词或问题>" {
		t.Fatalf("empty search calls=%d requests=%d sent=%+v", retriever.calls, len(fake.requests), sender.Sent())
	}
}

func TestEngineDisabledSearchDoesNotExposeTool(t *testing.T) {
	engine, fake, retriever, sender := searchEngineFixture(t, []llm.Response{{Text: "direct", Message: llm.TextMessage(llm.RoleAssistant, "direct")}})
	engine.deps.Policy.Search.Enabled = false
	if _, err := engine.HandleInbound(context.Background(), testInbound("hello")); err != nil {
		t.Fatal(err)
	}
	if retriever.calls != 0 || len(fake.requests[0].Tools) != 0 || sender.Sent()[0].Text != "direct" {
		t.Fatalf("requests=%+v sent=%+v", fake.requests, sender.Sent())
	}
}

func TestEngineExplicitSearchReportsRetrieverFailure(t *testing.T) {
	engine, fake, retriever, sender := searchEngineFixture(t, nil)
	retriever.err = context.DeadlineExceeded
	if _, err := engine.HandleInbound(context.Background(), testInbound("/search authentication")); err != nil {
		t.Fatal(err)
	}
	if retriever.calls != 1 || len(fake.requests) != 0 {
		t.Fatalf("retriever calls=%d llm requests=%d", retriever.calls, len(fake.requests))
	}
	if sent := sender.Sent(); len(sent) != 1 || sent[0].Text != "文档检索失败，请稍后重试。" {
		t.Fatalf("sent = %+v", sent)
	}
}

type fakeLLM struct {
	request   llm.Request
	requests  []llm.Request
	responses []llm.Response
	response  llm.Response
	err       error
}

func (f *fakeLLM) Protocol() llm.Protocol { return llm.OpenAIResponses }
func (f *fakeLLM) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	f.request = request
	f.requests = append(f.requests, request)
	if len(f.responses) > 0 {
		response := f.responses[0]
		f.responses = f.responses[1:]
		return response, f.err
	}
	return f.response, f.err
}

type fakeRetriever struct {
	calls    int
	response searchcontract.Response
	err      error
}

func (f *fakeRetriever) Search(context.Context, searchcontract.Query) (searchcontract.Response, error) {
	f.calls++
	return f.response, f.err
}
func (f *fakeRetriever) Reload(context.Context) (searchcontract.ReloadResult, error) {
	return searchcontract.ReloadResult{}, nil
}
func (f *fakeRetriever) Status() searchcontract.Status {
	return searchcontract.Status{Enabled: true, State: searchcontract.StateReady}
}

func searchEngineFixture(t *testing.T, responses []llm.Response) (*Engine, *fakeLLM, *fakeRetriever, *memory.Sender) {
	t.Helper()
	registry := platform.NewRegistry()
	sender := memory.NewSender(slog.Default())
	if err := registry.RegisterConnector("echo", sender); err != nil {
		t.Fatal(err)
	}
	if err := registry.BindConversation(platform.ConversationBinding{ConversationID: "c", ConnectorID: "echo", Target: "c"}); err != nil {
		t.Fatal(err)
	}
	retriever := &fakeRetriever{response: searchcontract.Response{
		Query: "authentication",
		Results: []searchcontract.Result{{
			SectionID: "1", Path: "auth.md", Headings: []string{"Authentication"},
			Snippet: "Use an API key.", Score: 1, MatchRatio: 1,
		}},
	}}
	tool := searchdocs.New(retriever, 5, 8, 12000)
	fake := &fakeLLM{responses: responses}
	policy := testPolicy()
	policy.Model = "test-model"
	policy.Search = SearchPolicy{Enabled: true, DefaultResults: 5, MaxResults: 8, MaxQueriesPerTurn: 1, MaxContextChars: 12000}
	engine, err := NewEngine(context.Background(), Dependencies{Sender: registry, LLM: fake, Retriever: retriever, Tools: tooling.NewRegistry(tool), Policy: policy}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	return engine, fake, retriever, sender
}

func testInbound(text string) messaging.InboundMessage {
	return messaging.InboundMessage{ConversationID: "c", UserID: "u", MessageID: "m", Text: text, Command: strings.HasPrefix(text, "/")}
}
func (f *fakeLLM) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

func testPolicy() Policy {
	return Policy{Name: "ForgeBot", Gate: GatePolicy{
		Silence: 4 * time.Second, MaxCount: 8, MaxWindow: 25 * time.Second,
		DedupTTL: time.Minute, DedupMax: 10000, MaxBuffered: 50, MaxTextLen: 500, MaxConcurrent: 4,
	}}
}
