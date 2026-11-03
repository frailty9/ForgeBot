package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/operation"
)

// Transport carries MCP JSON-RPC messages. The MCP client deliberately does
// not depend on a wire transport so stdio can be added without changing tool
// discovery or execution.
type Transport interface {
	Call(context.Context, []byte) ([]byte, error)
	Notify(context.Context, []byte) error
	Close() error
}

type HTTPConfig struct {
	Endpoint         string
	APIKey           string
	Timeout          time.Duration
	MaxResponseBytes int64
}

type streamableHTTPTransport struct {
	endpoint         string
	apiKey           string
	http             *http.Client
	maxResponseBytes int64
	mu               sync.Mutex
	session          string
	closed           bool
}

func NewStreamableHTTPTransport(cfg HTTPConfig) (Transport, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, errors.New("MCP endpoint is required")
	}
	if !strings.HasPrefix(cfg.Endpoint, "http://") && !strings.HasPrefix(cfg.Endpoint, "https://") {
		return nil, errors.New("MCP endpoint must use http or https")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = 1 << 20
	}
	return &streamableHTTPTransport{
		endpoint: cfg.Endpoint, apiKey: cfg.APIKey,
		http: &http.Client{Timeout: cfg.Timeout}, maxResponseBytes: cfg.MaxResponseBytes,
	}, nil
}

func (t *streamableHTTPTransport) Call(ctx context.Context, body []byte) ([]byte, error) {
	return t.request(ctx, body, false)
}

func (t *streamableHTTPTransport) Notify(ctx context.Context, body []byte) error {
	_, err := t.request(ctx, body, true)
	return err
}

func (t *streamableHTTPTransport) request(ctx context.Context, body []byte, notification bool) ([]byte, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, errors.New("MCP transport is closed")
	}
	session := t.session
	t.mu.Unlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if t.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	if session != "" {
		request.Header.Set("Mcp-Session-Id", session)
	}
	response, err := t.http.Do(request)
	if err != nil {
		return nil, operation.Wrap("mcp.request", operation.Unavailable, operation.EffectUnknown, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, t.maxResponseBytes))
		return nil, operation.Wrap("mcp.request", operation.HTTPCode(response.StatusCode), operation.EffectUnknown, fmt.Errorf("HTTP %s: %s", response.Status, strings.TrimSpace(string(data))))
	}
	if value := response.Header.Get("Mcp-Session-Id"); value != "" {
		t.mu.Lock()
		t.session = value
		t.mu.Unlock()
	}
	if notification {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, t.maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > t.maxResponseBytes {
		return nil, fmt.Errorf("MCP response exceeds %d bytes", t.maxResponseBytes)
	}
	if len(data) == 0 {
		return nil, errors.New("empty MCP response")
	}
	return data, nil
}

func (t *streamableHTTPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	t.http.CloseIdleConnections()
	return nil
}

var _ Transport = (*streamableHTTPTransport)(nil)
