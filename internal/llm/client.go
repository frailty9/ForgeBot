package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	defaultOpenAIBaseURL    = "https://api.openai.com/v1"
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultAnthropicVersion = "2023-06-01"
	defaultClientTimeout    = 60 * time.Second
	headerAuthorization     = "Authorization"
	headerContentType       = "Content-Type"
	headerAnthropicAPIKey   = "x-api-key"
	headerAnthropicVersion  = "anthropic-version"
	contentTypeJSON         = "application/json"
)

type ClientOption func(*clientOptions)

type clientOptions struct {
	httpClient *http.Client
}

func WithHTTPClient(client *http.Client) ClientOption {
	return func(opts *clientOptions) {
		opts.httpClient = client
	}
}

// NewClient constructs the implementation for cfg.Protocol.
func NewClient(cfg ClientConfig, options ...ClientOption) (Client, error) {
	if !cfg.Protocol.Valid() {
		return nil, fmt.Errorf("unsupported LLM protocol: %s", cfg.Protocol)
	}
	opts := clientOptions{}
	for _, option := range options {
		option(&opts)
	}
	httpClient := opts.httpClient
	if httpClient == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = defaultClientTimeout
		}
		httpClient = &http.Client{Timeout: timeout}
	}

	switch cfg.Protocol {
	case AnthropicMessages:
		return newAnthropicMessagesClient(cfg, httpClient), nil
	case OpenAIResponses:
		return newOpenAIResponsesClient(cfg, httpClient), nil
	case OpenAICompletions:
		return newOpenAICompletionsClient(cfg, httpClient), nil
	default:
		return nil, fmt.Errorf("unsupported LLM protocol: %s", cfg.Protocol)
	}
}

func endpointURL(baseURL, path string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func contextWithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	// WithTimeout 自动采用更早的父截止时间；有父预算不代表单次模型时限失效。
	return context.WithTimeout(ctx, timeout)
}
