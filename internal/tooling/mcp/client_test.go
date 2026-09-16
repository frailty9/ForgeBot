package mcp

import (
	"context"
	"encoding/json"
	"github.com/soleapi/forgebot/internal/tooling"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientDiscoversAndExecutesAllowedTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch request["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"tools": []any{map[string]any{"name": "get_model_pricing", "description": "pricing", "inputSchema": map[string]any{"type": "object"}}, map[string]any{"name": "hidden"}}}})
		case "tools/call":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "model price: 1"}}}})
		default:
			t.Errorf("unexpected method %v", request["method"])
		}
	}))
	defer server.Close()
	client, err := New(context.Background(), Config{ID: "platform", Endpoint: server.URL, AllowedTools: []string{"get_model_pricing"}, Timeout: time.Second, MaxResponseBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	definitions := client.Definitions()
	if len(definitions) != 1 || definitions[0].Name != "mcp.platform.get_model_pricing" {
		t.Fatalf("definitions=%+v", definitions)
	}
	result := client.Execute(context.Background(), tooling.Call{Name: "mcp.platform.get_model_pricing", Arguments: `{}`})
	if !result.OK || result.Payload != "model price: 1" {
		t.Fatalf("result=%+v", result)
	}
}
