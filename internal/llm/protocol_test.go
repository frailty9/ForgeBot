package llm

import (
	"context"
	"testing"
)

func TestParseProtocolOnlyAcceptsSupportedFamilies(t *testing.T) {
	tests := []struct {
		input string
		want  Protocol
		ok    bool
	}{
		{input: "anthropic_messages", want: AnthropicMessages, ok: true},
		{input: "openai_responses", want: OpenAIResponses, ok: true},
		{input: "openai_completions", want: OpenAICompletions, ok: true},
		{input: "OpenAI_Responses", want: OpenAIResponses, ok: true},
		{input: "gemini", ok: false},
		{input: "ollama", ok: false},
	}

	for _, test := range tests {
		got, err := ParseProtocol(test.input)
		if test.ok {
			if err != nil || got != test.want {
				t.Fatalf("ParseProtocol(%q) = %q, %v; want %q", test.input, got, err, test.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("ParseProtocol(%q) unexpectedly succeeded", test.input)
		}
	}
}

func TestValidateRequest(t *testing.T) {
	if err := ValidateRequest(Request{Model: "model", Messages: []Message{{Role: RoleUser}}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequest(Request{Model: "model", Instructions: "answer"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequest(Request{Messages: []Message{{Role: RoleUser}}}); err == nil {
		t.Fatal("missing model accepted")
	}
}

type fakeClient struct{}

func (fakeClient) Protocol() Protocol { return OpenAIResponses }
func (fakeClient) Complete(context.Context, Request) (Response, error) {
	return Response{}, nil
}
func (fakeClient) Stream(context.Context, Request) (<-chan StreamEvent, error) {
	return make(chan StreamEvent), nil
}

func TestClientProtocolIsExplicit(t *testing.T) {
	var client Client = fakeClient{}
	if client.Protocol() != OpenAIResponses {
		t.Fatalf("protocol = %s", client.Protocol())
	}
}

func TestToolMessageConstructors(t *testing.T) {
	assistant := AssistantToolCallMessage(Message{ToolCalls: []ToolCall{{ID: "call-1", Name: "search_docs", Arguments: `{"query":"auth"}`}}})
	if assistant.Role != RoleAssistant || len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant = %+v", assistant)
	}
	result := ToolResultMessage("call-1", "search_docs", `{"ok":true}`)
	if result.Role != RoleTool || result.ToolCallID != "call-1" || result.Name != "search_docs" || result.Text() != `{"ok":true}` {
		t.Fatalf("result = %+v", result)
	}
}
