package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type anthropicMessagesClient struct {
	cfg        ClientConfig
	httpClient *http.Client
}

var _ Client = (*anthropicMessagesClient)(nil)

func newAnthropicMessagesClient(cfg ClientConfig, httpClient *http.Client) Client {
	return &anthropicMessagesClient{cfg: cfg, httpClient: httpClient}
}

func (c *anthropicMessagesClient) Protocol() Protocol { return AnthropicMessages }

func (c *anthropicMessagesClient) Complete(ctx context.Context, req Request) (Response, error) {
	var err error
	if req, err = prepareRequest(c.cfg, req); err != nil {
		return Response{}, err
	}
	ctx, cancel := contextWithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	var raw map[string]any
	if err := doJSON(ctx, c.httpClient, http.MethodPost, endpointURL(c.baseURL(), "/v1/messages"),
		requestHeaders(c.cfg, AnthropicMessages), c.payload(req, false), &raw); err != nil {
		return Response{}, err
	}
	return normalizeAnthropicResponse(raw), nil
}

func (c *anthropicMessagesClient) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	var err error
	if req, err = prepareRequest(c.cfg, req); err != nil {
		return nil, err
	}
	ctx, cancel := contextWithTimeout(ctx, c.cfg.Timeout)
	resp, err := streamJSON(ctx, c.httpClient, http.MethodPost, endpointURL(c.baseURL(), "/v1/messages"),
		requestHeaders(c.cfg, AnthropicMessages), c.payload(req, true))
	if err != nil {
		cancel()
		return nil, err
	}
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		defer cancel()
		defer resp.Body.Close()
		var messageID string
		var stopReason string
		var currentToolCall *ToolCall
		if err := readSSE(resp.Body, func(event sseEvent) error {
			raw, err := decodeMap(event.Data)
			if err != nil {
				events <- StreamEvent{Error: err.Error()}
				return nil
			}
			eventType := firstNonEmpty(event.Event, jsonString(raw, "type"))
			switch eventType {
			case "message_start":
				messageID = jsonString(raw, "message", "id")
			case "content_block_start":
				block, _ := raw["content_block"].(map[string]any)
				if jsonString(block, "type") == "tool_use" {
					currentToolCall = &ToolCall{
						ID:   jsonString(block, "id"),
						Name: jsonString(block, "name"),
					}
					events <- StreamEvent{ID: messageID, ToolCall: currentToolCall}
				}
			case "content_block_delta":
				delta, _ := raw["delta"].(map[string]any)
				if jsonString(delta, "type") == "input_json_delta" && currentToolCall != nil {
					events <- StreamEvent{
						ID: messageID,
						ToolCall: &ToolCall{
							ID:        currentToolCall.ID,
							Name:      currentToolCall.Name,
							Arguments: jsonString(delta, "partial_json"),
						},
					}
				} else {
					events <- StreamEvent{
						ID:    messageID,
						Delta: jsonString(raw, "delta", "text"),
					}
				}
			case "message_delta":
				stopReason = jsonString(raw, "delta", "stop_reason")
			case "message_stop":
				events <- StreamEvent{
					ID:   messageID,
					Done: true,
					Response: &Response{
						ID:           messageID,
						Message:      Message{Role: RoleAssistant},
						FinishReason: stopReason,
					},
				}
			case "error":
				events <- StreamEvent{Done: true, Error: firstNonEmpty(
					jsonString(raw, "error", "message"),
					jsonString(raw, "message"),
					"Anthropic Messages stream failed",
				)}
			}
			return nil
		}); err != nil {
			events <- StreamEvent{Done: true, Error: err.Error()}
		}
	}()
	return events, nil
}

func (c *anthropicMessagesClient) baseURL() string {
	if strings.TrimSpace(c.cfg.BaseURL) == "" {
		return defaultAnthropicBaseURL
	}
	return c.cfg.BaseURL
}

func (c *anthropicMessagesClient) payload(req Request, stream bool) map[string]any {
	messages := make([]map[string]any, 0, len(req.Messages))
	system := strings.TrimSpace(req.Instructions)
	for _, message := range req.Messages {
		if message.Role == RoleSystem {
			if text := message.Text(); text != "" {
				if system != "" {
					system += "\n\n"
				}
				system += text
			}
			continue
		}
		if message.Role == RoleTool {
			block := map[string]any{
				"type": "tool_result", "tool_use_id": message.ToolCallID,
				"content": message.Text(),
			}
			if len(messages) > 0 && messages[len(messages)-1]["role"] == "user" {
				if content, ok := messages[len(messages)-1]["content"].([]map[string]any); ok && len(content) > 0 && content[len(content)-1]["type"] == "tool_result" {
					messages[len(messages)-1]["content"] = append(content, block)
					continue
				}
			}
			messages = append(messages, map[string]any{"role": "user", "content": []map[string]any{block}})
			continue
		}
		if message.Role == RoleAssistant && len(message.ToolCalls) > 0 {
			content := make([]map[string]any, 0, len(message.ToolCalls)+1)
			if text := message.Text(); text != "" {
				content = append(content, map[string]any{"type": "text", "text": text})
			}
			for _, call := range message.ToolCalls {
				content = append(content, map[string]any{
					"type": "tool_use", "id": call.ID, "name": call.Name,
					"input": toolArguments(call.Arguments),
				})
			}
			messages = append(messages, map[string]any{"role": "assistant", "content": content})
			continue
		}
		messages = append(messages, map[string]any{
			"role":    string(message.Role),
			"content": stringOrParts(message.Content),
		})
	}
	if len(messages) == 0 {
		messages = append(messages, map[string]any{"role": "user", "content": ""})
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	payload := map[string]any{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": maxTokens,
		"stream":     stream,
	}
	if system != "" {
		payload["system"] = system
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if tools := toolDefinitionsAnthropic(req.Tools); len(tools) > 0 {
		payload["tools"] = tools
	}
	if len(req.Metadata) > 0 {
		payload["metadata"] = req.Metadata
	}
	return payload
}

func toolArguments(arguments string) any {
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err == nil {
		return value
	}
	return map[string]any{}
}

func normalizeAnthropicResponse(raw map[string]any) Response {
	result := Response{
		ID:           jsonString(raw, "id"),
		Model:        jsonString(raw, "model"),
		FinishReason: jsonString(raw, "stop_reason"),
	}
	if content, ok := raw["content"].([]any); ok {
		for _, item := range content {
			part, _ := item.(map[string]any)
			if text := jsonString(part, "text"); text != "" {
				result.Text += text
				result.Message.Content = append(result.Message.Content, ContentPart{Type: "text", Text: text})
			}
			if jsonString(part, "type") == "tool_use" {
				result.Message.ToolCalls = append(result.Message.ToolCalls, ToolCall{
					ID:        jsonString(part, "id"),
					Name:      jsonString(part, "name"),
					Arguments: jsonValue(part["input"]),
				})
			}
		}
	}
	result.Message.Role = RoleAssistant
	if usage, ok := raw["usage"].(map[string]any); ok {
		result.Usage = Usage{
			InputTokens:  intValue(usage["input_tokens"]),
			OutputTokens: intValue(usage["output_tokens"]),
		}
		result.Usage.TotalTokens = result.Usage.InputTokens + result.Usage.OutputTokens
	}
	return result
}
