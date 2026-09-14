package llm

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Protocol is the wire contract used by an LLM endpoint.
//
// ForgeBot deliberately supports protocol families rather than vendor SDKs.
// A vendor that exposes one of these contracts only needs configuration; it
// does not need a new core abstraction.
type Protocol string

const (
	AnthropicMessages Protocol = "anthropic_messages"
	OpenAIResponses   Protocol = "openai_responses"
	OpenAICompletions Protocol = "openai_completions"
)

func (p Protocol) String() string {
	return string(p)
}

func (p Protocol) Valid() bool {
	switch p {
	case AnthropicMessages, OpenAIResponses, OpenAICompletions:
		return true
	default:
		return false
	}
}

func ParseProtocol(value string) (Protocol, error) {
	canonical := strings.ToLower(strings.TrimSpace(value))
	switch canonical {
	case string(AnthropicMessages):
		return AnthropicMessages, nil
	case string(OpenAIResponses):
		return OpenAIResponses, nil
	case string(OpenAICompletions):
		return OpenAICompletions, nil
	default:
		return "", fmt.Errorf("unsupported LLM protocol %q; supported protocols: %s, %s, %s",
			value, AnthropicMessages, OpenAIResponses, OpenAICompletions)
	}
}

type ClientConfig struct {
	Protocol         Protocol
	BaseURL          string
	APIKey           string
	Model            string
	AnthropicVersion string
	Timeout          time.Duration
	DefaultHeaders   map[string]string
}

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MediaURL string `json:"media_url,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
}

type Message struct {
	Role       Role          `json:"role"`
	Content    []ContentPart `json:"content"`
	Name       string        `json:"name,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
}

func TextMessage(role Role, text string) Message {
	return Message{
		Role:    role,
		Content: []ContentPart{{Type: "text", Text: text}},
	}
}

func AssistantToolCallMessage(message Message) Message {
	message.Role = RoleAssistant
	message.Content = append([]ContentPart(nil), message.Content...)
	message.ToolCalls = append([]ToolCall(nil), message.ToolCalls...)
	return message
}

func ToolResultMessage(callID, name, payload string) Message {
	return Message{
		Role: RoleTool, Name: name, ToolCallID: callID,
		Content: []ContentPart{{Type: "text", Text: payload}},
	}
}

func (m Message) Text() string {
	var builder strings.Builder
	for _, part := range m.Content {
		if part.Text != "" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

type Request struct {
	Model        string            `json:"model"`
	Messages     []Message         `json:"messages"`
	Instructions string            `json:"instructions,omitempty"`
	MaxTokens    int               `json:"max_tokens,omitempty"`
	Temperature  *float64          `json:"temperature,omitempty"`
	Tools        []ToolDefinition  `json:"tools,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type Response struct {
	ID           string  `json:"id"`
	Model        string  `json:"model"`
	Message      Message `json:"message"`
	Text         string  `json:"text,omitempty"`
	FinishReason string  `json:"finish_reason,omitempty"`
	Usage        Usage   `json:"usage"`
}

type StreamEvent struct {
	ID       string    `json:"id,omitempty"`
	Delta    string    `json:"delta,omitempty"`
	ToolCall *ToolCall `json:"tool_call,omitempty"`
	Response *Response `json:"response,omitempty"`
	Error    string    `json:"error,omitempty"`
	Done     bool      `json:"done,omitempty"`
}

// Client is the protocol-neutral LLM boundary used by the bot engine.
// Implementations own HTTP serialization, authentication, retries, and
// protocol-specific response normalization.
type Client interface {
	Protocol() Protocol
	Complete(context.Context, Request) (Response, error)
	Stream(context.Context, Request) (<-chan StreamEvent, error)
}

func ValidateRequest(req Request) error {
	if strings.TrimSpace(req.Model) == "" {
		return fmt.Errorf("LLM model is required")
	}
	if len(req.Messages) == 0 && strings.TrimSpace(req.Instructions) == "" {
		return fmt.Errorf("LLM request requires messages or instructions")
	}
	if req.MaxTokens < 0 {
		return fmt.Errorf("LLM max_tokens must not be negative")
	}
	if req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) {
		return fmt.Errorf("LLM temperature must be between 0 and 2")
	}
	return nil
}
