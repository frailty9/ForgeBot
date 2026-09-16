package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/tooling"
)

func TestMCPStdioTransportProcessRoundTrip(t *testing.T) {
	command := os.Args[0]
	transport, err := NewStdioTransport(StdioConfig{
		Command: command, Args: []string{"-test.run=TestMCPStdioHelper", "--"},
		Environment: map[string]string{"FORGEBOT_MCP_STDIO_HELPER": "1"},
		Timeout:     time.Second, MaxResponseBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewWithTransport(context.Background(), Config{ID: "local", AllowedTools: []string{"echo"}}, transport)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result := client.Execute(context.Background(), tooling.Call{Name: "mcp.local.echo", Arguments: `{"text":"hello"}`})
	if !result.OK || result.Payload != "hello" {
		t.Fatalf("result=%+v", result)
	}
}

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("FORGEBOT_MCP_STDIO_HELPER") != "1" {
		return
	}
	reader := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	for reader.Scan() {
		var request struct {
			JSONRPC string         `json:"jsonrpc"`
			ID      any            `json:"id"`
			Method  string         `json:"method"`
			Params  map[string]any `json:"params"`
		}
		if json.Unmarshal(reader.Bytes(), &request) != nil {
			continue
		}
		if request.Method == "notifications/initialized" {
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}
		case "tools/list":
			response["result"] = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "echo", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			args, _ := request.Params["arguments"].(map[string]any)
			response["result"] = map[string]any{"content": []any{map[string]any{"type": "text", "text": args["text"]}}}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		data, _ := json.Marshal(response)
		_, _ = writer.Write(append(data, '\n'))
		_ = writer.Flush()
	}
}
