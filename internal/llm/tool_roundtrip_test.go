package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestClientsSerializeToolRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		protocol Protocol
		path     string
		assert   func(*testing.T, map[string]any)
	}{
		{name: "openai_completions", protocol: OpenAICompletions, path: "/chat/completions", assert: assertCompletionsToolRoundTrip},
		{name: "openai_responses", protocol: OpenAIResponses, path: "/responses", assert: assertResponsesToolRoundTrip},
		{name: "anthropic_messages", protocol: AnthropicMessages, path: "/v1/messages", assert: assertAnthropicToolRoundTrip},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newJSONTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path {
					t.Fatalf("path = %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				test.assert(t, body)
				writeToolRoundTripResponse(w, test.protocol)
			})
			defer server.Close()
			client, err := NewClient(ClientConfig{Protocol: test.protocol, BaseURL: server.URL, Model: "model"}, WithHTTPClient(server.Client()))
			if err != nil {
				t.Fatal(err)
			}
			request := Request{Messages: []Message{
				TextMessage(RoleUser, "auth?"),
				AssistantToolCallMessage(Message{ToolCalls: []ToolCall{{ID: "call-1", Name: "search_docs", Arguments: `{"query":"auth"}`}}}),
				ToolResultMessage("call-1", "search_docs", `{"ok":true}`),
			}}
			if _, err := client.Complete(context.Background(), request); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func assertCompletionsToolRoundTrip(t *testing.T, body map[string]any) {
	t.Helper()
	messages := body["messages"].([]any)
	assistant := messages[1].(map[string]any)
	calls := assistant["tool_calls"].([]any)
	if calls[0].(map[string]any)["id"] != "call-1" {
		t.Fatalf("assistant = %#v", assistant)
	}
	tool := messages[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call-1" || tool["content"] != `{"ok":true}` {
		t.Fatalf("tool = %#v", tool)
	}
}

func assertResponsesToolRoundTrip(t *testing.T, body map[string]any) {
	t.Helper()
	input := body["input"].([]any)
	call := input[1].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "call-1" || call["name"] != "search_docs" {
		t.Fatalf("call = %#v", call)
	}
	result := input[2].(map[string]any)
	if result["type"] != "function_call_output" || result["call_id"] != "call-1" || result["output"] != `{"ok":true}` {
		t.Fatalf("result = %#v", result)
	}
}

func assertAnthropicToolRoundTrip(t *testing.T, body map[string]any) {
	t.Helper()
	messages := body["messages"].([]any)
	assistant := messages[1].(map[string]any)
	blocks := assistant["content"].([]any)
	call := blocks[0].(map[string]any)
	if call["type"] != "tool_use" || call["id"] != "call-1" || call["name"] != "search_docs" {
		t.Fatalf("call = %#v", call)
	}
	input := call["input"].(map[string]any)
	if input["query"] != "auth" {
		t.Fatalf("input = %#v", input)
	}
	resultBlocks := messages[2].(map[string]any)["content"].([]any)
	result := resultBlocks[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "call-1" || result["content"] != `{"ok":true}` {
		t.Fatalf("result = %#v", result)
	}
}

func writeToolRoundTripResponse(w http.ResponseWriter, protocol Protocol) {
	switch protocol {
	case OpenAICompletions:
		writeJSONTest(w, map[string]any{
			"id": "done",
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "done"},
			}},
		})
	case OpenAIResponses:
		writeJSONTest(w, map[string]any{"id": "done", "output_text": "done", "status": "completed"})
	case AnthropicMessages:
		writeJSONTest(w, map[string]any{
			"id": "done", "content": []any{map[string]any{"type": "text", "text": "done"}},
		})
	}
}
