// Package mcp adapts MCP JSON-RPC servers to ForgeBot's allow-listed tool
// executor. Transport details are hidden behind Transport.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/tooling"
)

// Config selects an MCP server identity, credentials, and exposed tools.
type Config struct {
	ID               string
	Endpoint         string
	APIKey           string
	AllowedTools     []string
	Timeout          time.Duration
	MaxResponseBytes int64
}

// Client discovers and invokes the configured subset of an MCP server's tools.
type Client struct {
	cfg       Config
	transport Transport
	mu        sync.Mutex
	nextID    int64
	tools     map[string]tooling.Definition
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// New connects to a Streamable HTTP MCP server and discovers allowed tools.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.ID) == "" || strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, errors.New("MCP id and endpoint are required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = 1 << 20
	}
	transport, err := NewStreamableHTTPTransport(HTTPConfig{Endpoint: cfg.Endpoint, APIKey: cfg.APIKey, Timeout: cfg.Timeout, MaxResponseBytes: cfg.MaxResponseBytes})
	if err != nil {
		return nil, err
	}
	c := &Client{cfg: cfg, transport: transport, tools: make(map[string]tooling.Definition)}
	if err := c.initialize(ctx); err != nil {
		_ = transport.Close()
		return nil, err
	}
	return c, nil
}

// NewWithStdio starts a local MCP process and discovers its allowed tools.
func NewWithStdio(ctx context.Context, cfg Config, stdio StdioConfig) (*Client, error) {
	transport, err := NewStdioTransport(stdio)
	if err != nil {
		return nil, err
	}
	return NewWithTransport(ctx, cfg, transport)
}

// NewWithTransport creates a client over an already constructed transport.
func NewWithTransport(ctx context.Context, cfg Config, transport Transport) (*Client, error) {
	if strings.TrimSpace(cfg.ID) == "" {
		return nil, errors.New("MCP id is required")
	}
	if transport == nil {
		return nil, errors.New("MCP transport is required")
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = 1 << 20
	}
	c := &Client{cfg: cfg, transport: transport, tools: make(map[string]tooling.Definition)}
	if err := c.initialize(ctx); err != nil {
		_ = transport.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) initialize(ctx context.Context) error {
	var result struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      map[string]any `json:"serverInfo"`
	}
	if err := c.call(ctx, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "ForgeBot", "version": "0.1"}}, &result); err != nil {
		return fmt.Errorf("MCP initialize: %w", err)
	}
	if err := c.notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("MCP initialized notification: %w", err)
	}
	var listed struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]any{}, &listed); err != nil {
		return fmt.Errorf("MCP tools/list: %w", err)
	}
	allowed := make(map[string]struct{}, len(c.cfg.AllowedTools))
	for _, name := range c.cfg.AllowedTools {
		allowed[strings.TrimSpace(name)] = struct{}{}
	}
	for _, item := range listed.Tools {
		if item.Name == "" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[item.Name]; !ok {
				continue
			}
		}
		name := "mcp." + c.cfg.ID + "." + item.Name
		c.tools[name] = tooling.Definition{Name: name, Description: item.Description, InputSchema: item.InputSchema}
	}
	if len(c.tools) == 0 {
		return fmt.Errorf("MCP server %q exposed no allowed tools", c.cfg.ID)
	}
	return nil
}

// Definitions returns stable, sorted names for tools exposed to the LLM.
func (c *Client) Definitions() []tooling.Definition {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]tooling.Definition, 0, len(c.tools))
	for _, definition := range c.tools {
		result = append(result, definition)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// Execute validates the namespaced tool name and forwards one MCP tools/call.
func (c *Client) Execute(ctx context.Context, call tooling.Call) tooling.Result {
	c.mu.Lock()
	_, ok := c.tools[call.Name]
	c.mu.Unlock()
	if !ok {
		return tooling.Result{Code: "tool_not_allowed"}
	}
	prefix := "mcp." + c.cfg.ID + "."
	toolName := strings.TrimPrefix(call.Name, prefix)
	if toolName == call.Name || toolName == "" {
		return tooling.Result{Code: "invalid_tool_name"}
	}
	var args map[string]any
	decoder := json.NewDecoder(strings.NewReader(call.Arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return tooling.Result{Code: "invalid_arguments"}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return tooling.Result{Code: "invalid_arguments"}
	}
	var result struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text,omitempty"`
			Data     string `json:"data,omitempty"`
			MimeType string `json:"mimeType,omitempty"`
		} `json:"content"`
		IsError    bool `json:"isError,omitempty"`
		Structured any  `json:"structuredContent,omitempty"`
	}
	if err := c.call(ctx, "tools/call", map[string]any{"name": toolName, "arguments": args}, &result); err != nil {
		return tooling.Result{Code: "mcp_call_failed", Payload: err.Error()}
	}
	if result.IsError {
		return tooling.Result{Code: "mcp_tool_error", Payload: renderContent(result.Content)}
	}
	payload := renderContent(result.Content)
	if payload == "" && result.Structured != nil {
		encoded, _ := json.Marshal(result.Structured)
		payload = string(encoded)
	}
	return tooling.Result{OK: true, Payload: payload}
}

func renderContent(content []struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}) string {
	var builder strings.Builder
	for _, item := range content {
		if item.Text != "" {
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(item.Text)
		}
	}
	return builder.String()
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	data, err := c.transport.Call(ctx, body)
	if err != nil {
		return err
	}
	var envelope rpcEnvelope
	if err := decodeEnvelope(data, &envelope); err != nil {
		return err
	}
	if envelope.Error != nil {
		return fmt.Errorf("rpc %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("decode result: %w", err)
	}
	return nil
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	return c.transport.Notify(ctx, body)
}

func decodeEnvelope(data []byte, envelope *rpcEnvelope) error {
	if err := json.Unmarshal(data, envelope); err == nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "data:") {
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), envelope); err == nil {
				return nil
			}
		}
	}
	return errors.New("invalid MCP JSON response")
}

func (c *Client) Close() error {
	if c == nil || c.transport == nil {
		return nil
	}
	return c.transport.Close()
}

var _ tooling.Executor = (*Client)(nil)
