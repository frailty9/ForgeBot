package llm

import (
	"context"
	"net/http"
	"strings"
)

type openAIResponsesClient struct {
	cfg        ClientConfig
	httpClient *http.Client
}

var _ Client = (*openAIResponsesClient)(nil)

func newOpenAIResponsesClient(cfg ClientConfig, httpClient *http.Client) Client {
	return &openAIResponsesClient{cfg: cfg, httpClient: httpClient}
}

func (c *openAIResponsesClient) Protocol() Protocol { return OpenAIResponses }

func (c *openAIResponsesClient) Complete(ctx context.Context, req Request) (Response, error) {
	var err error
	if req, err = prepareRequest(c.cfg, req); err != nil {
		return Response{}, err
	}
	ctx, cancel := contextWithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	var raw map[string]any
	if err := doJSON(ctx, c.httpClient, http.MethodPost, endpointURL(c.baseURL(), "/responses"),
		requestHeaders(c.cfg, OpenAIResponses), c.payload(req, false), &raw); err != nil {
		return Response{}, err
	}
	return normalizeOpenAIResponse(raw), nil
}

func (c *openAIResponsesClient) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	var err error
	if req, err = prepareRequest(c.cfg, req); err != nil {
		return nil, err
	}
	ctx, cancel := contextWithTimeout(ctx, c.cfg.Timeout)
	resp, err := streamJSON(ctx, c.httpClient, http.MethodPost, endpointURL(c.baseURL(), "/responses"),
		requestHeaders(c.cfg, OpenAIResponses), c.payload(req, true))
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
			raw, err := decodeMap(event.Data)
			if err != nil {
				events <- StreamEvent{Error: err.Error()}
				return nil
			}
			eventType := firstNonEmpty(event.Event, jsonString(raw, "type"))
			switch eventType {
			case "response.output_text.delta":
				events <- StreamEvent{ID: jsonString(raw, "response_id"), Delta: jsonString(raw, "delta")}
			case "response.function_call_arguments.delta":
				events <- StreamEvent{
					ID: jsonString(raw, "response_id"),
					ToolCall: &ToolCall{
						ID:        firstNonEmpty(jsonString(raw, "call_id"), jsonString(raw, "item_id")),
						Name:      jsonString(raw, "name"),
						Arguments: jsonString(raw, "delta"),
					},
				}
			case "response.completed":
				responseRaw, _ := raw["response"].(map[string]any)
				response := normalizeOpenAIResponse(responseRaw)
				events <- StreamEvent{ID: response.ID, Done: true, Response: &response}
			case "error", "response.failed":
				events <- StreamEvent{Done: true, Error: firstNonEmpty(
					jsonString(raw, "message"),
					jsonString(raw, "error", "message"),
					"OpenAI Responses stream failed",
				)}
			}
			return nil
		}); err != nil {
			events <- StreamEvent{Done: true, Error: err.Error()}
		}
	}()
	return events, nil
}

func (c *openAIResponsesClient) baseURL() string {
	if strings.TrimSpace(c.cfg.BaseURL) == "" {
		return defaultOpenAIBaseURL
	}
	return c.cfg.BaseURL
}

func (c *openAIResponsesClient) payload(req Request, stream bool) map[string]any {
	input := make([]map[string]any, 0, len(req.Messages))
	instructions := strings.TrimSpace(req.Instructions)
	for _, message := range req.Messages {
		if message.Role == RoleSystem {
			if instructions == "" {
				instructions = message.Text()
			} else if text := message.Text(); text != "" {
				instructions += "\n\n" + text
			}
			continue
		}
		item := map[string]any{
			"role":    string(message.Role),
			"content": responsesContent(message.Content),
		}
		input = append(input, item)
	}
	payload := map[string]any{
		"model":  req.Model,
		"input":  input,
		"stream": stream,
	}
	if instructions != "" {
		payload["instructions"] = instructions
	}
	if req.MaxTokens > 0 {
		payload["max_output_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if tools := toolDefinitionsResponses(req.Tools); len(tools) > 0 {
		payload["tools"] = tools
	}
	if len(req.Metadata) > 0 {
		payload["metadata"] = req.Metadata
	}
	return payload
}

func normalizeOpenAIResponse(raw map[string]any) Response {
	result := Response{
		ID:    jsonString(raw, "id"),
		Model: jsonString(raw, "model"),
		Text:  jsonString(raw, "output_text"),
	}
	if result.Text == "" {
		if output, ok := raw["output"].([]any); ok {
			for _, item := range output {
				object, _ := item.(map[string]any)
				content, _ := object["content"].([]any)
				for _, part := range content {
					partMap, _ := part.(map[string]any)
					if text := jsonString(partMap, "text"); text != "" {
						result.Text += text
					}
				}
			}
		}
	}
	result.Message = TextMessage(RoleAssistant, result.Text)
	if output, ok := raw["output"].([]any); ok {
		for _, item := range output {
			object, _ := item.(map[string]any)
			if jsonString(object, "type") == "function_call" {
				result.Message.ToolCalls = append(result.Message.ToolCalls, ToolCall{
					ID:        firstNonEmpty(jsonString(object, "call_id"), jsonString(object, "id")),
					Name:      jsonString(object, "name"),
					Arguments: jsonString(object, "arguments"),
				})
			}
		}
	}
	result.FinishReason = firstNonEmpty(jsonString(raw, "finish_reason"), jsonString(raw, "status"))
	if usage, ok := raw["usage"].(map[string]any); ok {
		result.Usage = Usage{
			InputTokens:  intValue(usage["input_tokens"]),
			OutputTokens: intValue(usage["output_tokens"]),
			TotalTokens:  intValue(usage["total_tokens"]),
		}
	}
	return result
}
