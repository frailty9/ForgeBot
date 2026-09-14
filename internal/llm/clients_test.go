package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testRequest() Request {
	return Request{
		Model:    "test-model",
		Messages: []Message{TextMessage(RoleUser, "hello")},
	}
}

func TestOpenAICompletionsClientCompleteAndStream(t *testing.T) {
	var completeBody map[string]any
	server := newJSONTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization = %q", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&completeBody)
		if completeBody["model"] != "test-model" {
			t.Fatalf("body = %#v", completeBody)
		}
		writeJSONTest(w, map[string]any{
			"id": "chatcmpl-1", "model": "test-model",
			"choices": []any{map[string]any{
				"message":       map[string]any{"role": "assistant", "content": "hello back"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5},
		})
	})
	defer server.Close()
	client, err := NewClient(ClientConfig{
		Protocol: OpenAICompletions, BaseURL: server.URL + "/v1", APIKey: "secret",
	}, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Complete(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.Text != "hello back" || response.Usage.TotalTokens != 5 {
		t.Fatalf("response = %+v", response)
	}

	streamServer := newSSETestServer(t, "/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("accept = %q", got)
		}
		writeSSETest(w, `{"id":"chatcmpl-2","choices":[{"delta":{"content":"hel"}}]}`)
		writeSSETest(w, `{"id":"chatcmpl-2","choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}]}`)
		writeSSETest(w, "[DONE]")
	})
	defer streamServer.Close()
	streamClient, err := NewClient(ClientConfig{
		Protocol: OpenAICompletions, BaseURL: streamServer.URL + "/v1",
	}, WithHTTPClient(streamServer.Client()))
	if err != nil {
		t.Fatal(err)
	}
	events, err := streamClient.Stream(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	var done bool
	for event := range events {
		deltas = append(deltas, event.Delta)
		done = done || event.Done
	}
	if strings.Join(deltas, "") != "hello" || !done {
		t.Fatalf("stream deltas=%q done=%v", deltas, done)
	}
}

func TestImageContentUsesProtocolSpecificBlocks(t *testing.T) {
	request := Request{
		Model: "vision-model",
		Messages: []Message{{Role: RoleUser, Content: []ContentPart{
			{Type: "text", Text: "describe this"},
			{Type: "image", MediaURL: "https://cdn.example/image.jpg", MimeType: "image/jpeg"},
		}}},
	}

	completions := (&openAICompletionsClient{}).payload(request, false)
	completionsMessages := completions["messages"].([]map[string]any)
	completionsContent := completionsMessages[0]["content"].([]map[string]any)
	if completionsContent[1]["type"] != "image_url" || completionsContent[1]["image_url"].(map[string]any)["url"] != "https://cdn.example/image.jpg" {
		t.Fatalf("completions image = %#v", completionsContent[1])
	}

	responses := (&openAIResponsesClient{}).payload(request, false)
	responsesInput := responses["input"].([]map[string]any)
	responsesContentParts := responsesInput[0]["content"].([]map[string]any)
	if responsesContentParts[1]["type"] != "input_image" || responsesContentParts[1]["image_url"] != "https://cdn.example/image.jpg" {
		t.Fatalf("responses image = %#v", responsesContentParts[1])
	}

	anthropic := (&anthropicMessagesClient{}).payload(request, false)
	anthropicMessages := anthropic["messages"].([]map[string]any)
	anthropicContentParts := anthropicMessages[0]["content"].([]map[string]any)
	if anthropicContentParts[1]["type"] != "image" {
		t.Fatalf("anthropic image = %#v", anthropicContentParts[1])
	}
	source := anthropicContentParts[1]["source"].(map[string]any)
	if source["type"] != "url" || source["url"] != "https://cdn.example/image.jpg" {
		t.Fatalf("anthropic source = %#v", source)
	}
}

func TestClientUsesConfiguredDefaultModel(t *testing.T) {
	server := newJSONTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "configured-model" {
			t.Fatalf("model = %#v", body["model"])
		}
		writeJSONTest(w, map[string]any{
			"id": "chatcmpl-model", "model": "configured-model",
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
		})
	})
	defer server.Close()
	client, err := NewClient(ClientConfig{
		Protocol: OpenAICompletions, BaseURL: server.URL + "/v1", Model: "configured-model",
	}, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), Request{
		Messages: []Message{TextMessage(RoleUser, "hello")},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIResponsesClientCompleteAndStream(t *testing.T) {
	server := newJSONTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization = %q", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "test-model" || body["stream"] != false {
			t.Fatalf("body = %#v", body)
		}
		writeJSONTest(w, map[string]any{
			"id": "resp-1", "model": "test-model", "output_text": "response text",
			"status": "completed",
			"usage":  map[string]any{"input_tokens": 4, "output_tokens": 6, "total_tokens": 10},
		})
	})
	defer server.Close()
	client, err := NewClient(ClientConfig{
		Protocol: OpenAIResponses, BaseURL: server.URL, APIKey: "secret",
	}, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Complete(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.Text != "response text" || response.Usage.InputTokens != 4 {
		t.Fatalf("response = %+v", response)
	}

	streamServer := newSSETestServer(t, "/responses", func(w http.ResponseWriter, r *http.Request) {
		writeSSEEventTest(w, "response.output_text.delta", `{"type":"response.output_text.delta","response_id":"resp-2","delta":"ok"}`)
		writeSSEEventTest(w, "response.completed", `{"type":"response.completed","response":{"id":"resp-2","model":"test-model","output_text":"ok","status":"completed"}}`)
	})
	defer streamServer.Close()
	streamClient, err := NewClient(ClientConfig{
		Protocol: OpenAIResponses, BaseURL: streamServer.URL,
	}, WithHTTPClient(streamServer.Client()))
	if err != nil {
		t.Fatal(err)
	}
	events, err := streamClient.Stream(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	var got string
	var completed bool
	for event := range events {
		got += event.Delta
		completed = completed || event.Done
	}
	if got != "ok" || !completed {
		t.Fatalf("stream text=%q completed=%v", got, completed)
	}
}

func TestAnthropicMessagesClientCompleteAndStream(t *testing.T) {
	server := newJSONTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "secret" {
			t.Fatalf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2024-01-01" {
			t.Fatalf("anthropic-version = %q", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["max_tokens"] != float64(1024) || body["system"] != "be concise" {
			t.Fatalf("body = %#v", body)
		}
		writeJSONTest(w, map[string]any{
			"id": "msg-1", "model": "claude-test", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "anthropic reply"}},
			"usage":   map[string]any{"input_tokens": 3, "output_tokens": 4},
		})
	})
	defer server.Close()
	request := testRequest()
	request.Messages = append([]Message{TextMessage(RoleSystem, "be concise")}, request.Messages...)
	client, err := NewClient(ClientConfig{
		Protocol: AnthropicMessages, BaseURL: server.URL, APIKey: "secret",
		AnthropicVersion: "2024-01-01",
	}, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Complete(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Text != "anthropic reply" || response.Usage.TotalTokens != 7 {
		t.Fatalf("response = %+v", response)
	}

	streamServer := newSSETestServer(t, "/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		writeSSEEventTest(w, "message_start", `{"type":"message_start","message":{"id":"msg-2"}}`)
		writeSSEEventTest(w, "content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`)
		writeSSEEventTest(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)
		writeSSEEventTest(w, "message_stop", `{"type":"message_stop"}`)
	})
	defer streamServer.Close()
	streamClient, err := NewClient(ClientConfig{
		Protocol: AnthropicMessages, BaseURL: streamServer.URL,
	}, WithHTTPClient(streamServer.Client()))
	if err != nil {
		t.Fatal(err)
	}
	events, err := streamClient.Stream(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	var got string
	var done bool
	for event := range events {
		got += event.Delta
		done = done || event.Done
	}
	if got != "hello" || !done {
		t.Fatalf("stream text=%q done=%v", got, done)
	}
}

func newJSONTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func newSSETestServer(t *testing.T, path string, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			t.Fatalf("path = %s, want %s", r.URL.Path, path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		handler(w, r)
	}))
}

func writeJSONTest(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeSSETest(w http.ResponseWriter, data string) {
	_, _ = w.Write([]byte("data: " + data + "\n\n"))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeSSEEventTest(w http.ResponseWriter, event, data string) {
	_, _ = w.Write([]byte("event: " + event + "\ndata: " + data + "\n\n"))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func TestStreamHonorsCallerCancellation(t *testing.T) {
	server := newSSETestServer(t, "/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
	})
	defer server.Close()
	client, err := NewClient(ClientConfig{
		Protocol: OpenAICompletions, BaseURL: server.URL + "/v1",
	}, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	events, err := client.Stream(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	for range events {
	}
}

func TestSSEParserAcceptsMultilineData(t *testing.T) {
	var got sseEvent
	err := readSSE(bufio.NewReader(strings.NewReader("event: test\ndata: {\"a\":\ndata: 1}\n\n")), func(event sseEvent) error {
		got = event
		return nil
	})
	if err != nil || got.Event != "test" || got.Data != "{\"a\":\n1}" {
		t.Fatalf("event = %+v, err=%v", got, err)
	}
}
