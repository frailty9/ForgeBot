package llm

import (
	"context"
	"net/http"
	"strings"
)

type openAICompletionsClient struct {
	cfg        ClientConfig
	httpClient *http.Client
}

var _ Client = (*openAICompletionsClient)(nil)

func newOpenAICompletionsClient(cfg ClientConfig, httpClient *http.Client) Client {
	return &openAICompletionsClient{cfg: cfg, httpClient: httpClient}
}

func (c *openAICompletionsClient) Protocol() Protocol { return OpenAICompletions }

func (c *openAICompletionsClient) Complete(ctx context.Context, req Request) (Response, error) {
	var err error
	if req, err = prepareRequest(c.cfg, req); err != nil {
		return Response{}, err
	}
	ctx, cancel := contextWithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	payload := c.payload(req, false)
	var raw map[string]any
	if err := doJSON(ctx, c.httpClient, http.MethodPost, endpointURL(c.baseURL(), "/chat/completions"),
		requestHeaders(c.cfg, OpenAICompletions), payload, &raw); err != nil {
		return Response{}, err
	}
	return normalizeOpenAICompletion(raw), nil
}

func (c *openAICompletionsClient) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	var err error
	if req, err = prepareRequest(c.cfg, req); err != nil {
		return nil, err
	}
	ctx, cancel := contextWithTimeout(ctx, c.cfg.Timeout)
	resp, err := streamJSON(ctx, c.httpClient, http.MethodPost, endpointURL(c.baseURL(), "/chat/completions"),
		requestHeaders(c.cfg, OpenAICompletions), c.payload(req, true))
	if err != nil {
		cancel()
		return nil, err
	}
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		defer cancel()
		defer resp.Body.Close()
		if err := readSSE(resp.Body, func(event sseEvent) error {
			if strings.TrimSpace(event.Data) == "[DONE]" {
				events <- StreamEvent{Done: true}
				return nil
			}
			raw, err := decodeMap(event.Data)
			if err != nil {
				events <- StreamEvent{Error: err.Error()}
				return nil
			}
			if errorMessage := firstNonEmpty(
				jsonString(raw, "error", "message"),
				jsonString(raw, "message"),
			); errorMessage != "" {
				events <- StreamEvent{Done: true, Error: errorMessage}
				return nil
			}
			choice, _ := raw["choices"].([]any)
			if len(choice) == 0 {
				return nil
			}
			first, _ := choice[0].(map[string]any)
			delta, _ := first["delta"].(map[string]any)
			text, _ := delta["content"].(string)
			if calls, ok := delta["tool_calls"].([]any); ok && len(calls) > 0 {
				call, _ := calls[0].(map[string]any)
				function, _ := call["function"].(map[string]any)
				events <- StreamEvent{
					ID: jsonString(raw, "id"),
					ToolCall: &ToolCall{
						ID:        jsonString(call, "id"),
						Name:      jsonString(function, "name"),
						Arguments: jsonString(function, "arguments"),
					},
				}
			}
			if text != "" {
				events <- StreamEvent{
					ID:    jsonString(raw, "id"),
					Delta: text,
				}
			}
			if reason := jsonString(first, "finish_reason"); reason != "" {
				events <- StreamEvent{
					ID:   jsonString(raw, "id"),
					Done: true,
					Response: &Response{
						ID:           jsonString(raw, "id"),
						Model:        jsonString(raw, "model"),
						FinishReason: reason,
					},
				}
			}
			if usage, ok := raw["usage"].(map[string]any); ok {
				events <- StreamEvent{
					ID: jsonString(raw, "id"),
					Response: &Response{
						ID:    jsonString(raw, "id"),
						Model: jsonString(raw, "model"),
						Usage: Usage{
							InputTokens:  intValue(usage["prompt_tokens"]),
							OutputTokens: intValue(usage["completion_tokens"]),
							TotalTokens:  intValue(usage["total_tokens"]),
						},
					},
				}
			}
			return nil
		}); err != nil {
			events <- StreamEvent{Done: true, Error: err.Error()}
		}
	}()
	return events, nil
}

func (c *openAICompletionsClient) baseURL() string {
	if strings.TrimSpace(c.cfg.BaseURL) == "" {
		return defaultOpenAIBaseURL
	}
	return c.cfg.BaseURL
}

func (c *openAICompletionsClient) payload(req Request, stream bool) map[string]any {
	messages := make([]map[string]any, 0, len(req.Messages)+1)
	if instructions := strings.TrimSpace(req.Instructions); instructions != "" {
		messages = append(messages, map[string]any{
			"role":    string(RoleSystem),
			"content": instructions,
		})
	}
	for _, message := range req.Messages {
		item := map[string]any{
			"role":    string(message.Role),
			"content": stringOrParts(message.Content),
		}
		if message.Name != "" {
			item["name"] = message.Name
		}
		if message.ToolCallID != "" {
			item["tool_call_id"] = message.ToolCallID
		}
		if len(message.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				calls = append(calls, map[string]any{
					"id":   call.ID,
					"type": "function",
					"function": map[string]any{
						"name":      call.Name,
						"arguments": call.Arguments,
					},
				})
			}
			item["tool_calls"] = calls
		}
		messages = append(messages, item)
	}
	payload := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   stream,
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if tools := toolDefinitionsOpenAI(req.Tools); len(tools) > 0 {
		payload["tools"] = tools
	}
	if len(req.Metadata) > 0 {
		payload["metadata"] = req.Metadata
	}
	return payload
}

func normalizeOpenAICompletion(raw map[string]any) Response {
	result := Response{
		ID:    jsonString(raw, "id"),
		Model: jsonString(raw, "model"),
	}
	if choices, ok := raw["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			result.FinishReason = jsonString(choice, "finish_reason")
			if message, ok := choice["message"].(map[string]any); ok {
				result.Message = messageFromOpenAI(message)
				result.Text = result.Message.Text()
			}
		}
	}
	if usage, ok := raw["usage"].(map[string]any); ok {
		result.Usage = Usage{
			InputTokens:  intValue(usage["prompt_tokens"]),
			OutputTokens: intValue(usage["completion_tokens"]),
			TotalTokens:  intValue(usage["total_tokens"]),
		}
	}
	return result
}

func messageFromOpenAI(raw map[string]any) Message {
	message := Message{
		Role: Role(jsonString(raw, "role")),
	}
	if message.Role == "" {
		message.Role = RoleAssistant
	}
	switch content := raw["content"].(type) {
	case string:
		message.Content = []ContentPart{{Type: "text", Text: content}}
	case []any:
		for _, item := range content {
			if part, ok := item.(map[string]any); ok {
				message.Content = append(message.Content, ContentPart{
					Type: jsonString(part, "type"),
					Text: jsonString(part, "text"),
				})
			}
		}
	}
	if calls, ok := raw["tool_calls"].([]any); ok {
		for _, item := range calls {
			call, _ := item.(map[string]any)
			fn, _ := call["function"].(map[string]any)
			message.ToolCalls = append(message.ToolCalls, ToolCall{
				ID:        jsonString(call, "id"),
				Name:      jsonString(fn, "name"),
				Arguments: jsonString(fn, "arguments"),
			})
		}
	}
	return message
}
