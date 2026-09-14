package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

func prepareRequest(cfg ClientConfig, req Request) (Request, error) {
	if strings.TrimSpace(req.Model) == "" {
		req.Model = strings.TrimSpace(cfg.Model)
	}
	if err := ValidateRequest(req); err != nil {
		return Request{}, err
	}
	return req, nil
}

func requestHeaders(cfg ClientConfig, protocol Protocol) map[string]string {
	headers := mergeHeaders(cfg.DefaultHeaders)
	switch protocol {
	case AnthropicMessages:
		if strings.TrimSpace(cfg.APIKey) != "" {
			headers[headerAnthropicAPIKey] = cfg.APIKey
		}
		version := strings.TrimSpace(cfg.AnthropicVersion)
		if version == "" {
			version = defaultAnthropicVersion
		}
		headers[headerAnthropicVersion] = version
	default:
		if strings.TrimSpace(cfg.APIKey) != "" {
			headers[headerAuthorization] = "Bearer " + cfg.APIKey
		}
	}
	return headers
}

func textParts(parts []ContentPart) string {
	var builder strings.Builder
	for _, part := range parts {
		if part.Text != "" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func stringOrParts(parts []ContentPart) any {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 && (parts[0].Type == "" || parts[0].Type == "text") {
		return textParts(parts)
	}
	out := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "", "text":
			out = append(out, map[string]any{"type": "text", "text": part.Text})
		case "image", "image_url":
			url := part.MediaURL
			if url == "" && part.Data != "" {
				url = "data:" + part.MimeType + ";base64," + part.Data
			}
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
		default:
			item := map[string]any{"type": part.Type}
			if part.Text != "" {
				item["text"] = part.Text
			}
			if part.MediaURL != "" {
				item["media_url"] = part.MediaURL
			}
			if part.MimeType != "" {
				item["mime_type"] = part.MimeType
			}
			if part.Data != "" {
				item["data"] = part.Data
			}
			out = append(out, item)
		}
	}
	return out
}

func responsesContent(parts []ContentPart) any {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 && (parts[0].Type == "" || parts[0].Type == "text") {
		return textParts(parts)
	}
	out := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "", "text":
			out = append(out, map[string]any{"type": "input_text", "text": part.Text})
		case "image", "image_url":
			url := part.MediaURL
			if url == "" && part.Data != "" {
				url = "data:" + part.MimeType + ";base64," + part.Data
			}
			out = append(out, map[string]any{"type": "input_image", "image_url": url})
		case "input_text", "input_image", "input_file":
			item := map[string]any{"type": part.Type}
			if part.Text != "" {
				item["text"] = part.Text
			}
			if part.MediaURL != "" {
				item["image_url"] = part.MediaURL
			}
			if part.Data != "" {
				item["image_url"] = "data:" + part.MimeType + ";base64," + part.Data
			}
			out = append(out, item)
		default:
			item := map[string]any{"type": part.Type}
			if part.Text != "" {
				item["text"] = part.Text
			}
			if part.MediaURL != "" {
				item["url"] = part.MediaURL
			}
			out = append(out, item)
		}
	}
	return out
}

func toolDefinitionsOpenAI(tools []ToolDefinition) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.InputSchema,
			},
		})
	}
	return out
}

func toolDefinitionsResponses(tools []ToolDefinition) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.InputSchema,
		})
	}
	return out
}

func toolDefinitionsAnthropic(tools []ToolDefinition) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": tool.InputSchema,
		})
	}
	return out
}

func jsonValue(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func intValue(value any) int {
	switch value := value.(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
