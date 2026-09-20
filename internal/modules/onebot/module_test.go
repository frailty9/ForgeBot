package onebot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/platform"
)

func TestProviderMergesDefaultsAndInstanceOverrides(t *testing.T) {
	module := json.RawMessage(`{"api_url":"http://default","send_token":"module-token","receive_secret":"module-secret","webhook_addr":"127.0.0.1:18090","webhook_path":"/onebot/default","request_limit":1024,"timeout":"10s","replay_ttl":"1m","replay_max":10}`)
	instance := json.RawMessage(`{"api_url":"http://instance","send_token":"","webhook_path":"/onebot/main","timeout":"20s"}`)
	options, err := merge(module, instance)
	if err != nil {
		t.Fatal(err)
	}
	if options.APIURL != "http://instance" || options.SendToken != "" || options.ReceiveSecret != "module-secret" || options.WebhookPath != "/onebot/main" || options.Timeout.String() != "20s" {
		t.Fatalf("merged options = %+v", options)
	}
}

func TestProviderRejectsInvalidOrIncompleteOptions(t *testing.T) {
	provider := provider{}
	bindings := []platform.ConnectorBinding{{ConversationID: "support", Target: "10001"}}
	if err := provider.ValidateOptions(nil, json.RawMessage(`{"timeout":"not-a-duration"}`), bindings); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("invalid duration error = %v", err)
	}
	if err := provider.ValidateOptions(nil, json.RawMessage(`{"api_url":"http://x"}`), bindings); err == nil || !strings.Contains(err.Error(), "webhook_addr is required") {
		t.Fatalf("incomplete error = %v", err)
	}
	if err := provider.ValidateOptions(json.RawMessage(`{"api_url":"http://x","webhook_addr":"127.0.0.1:18090","webhook_path":"/onebot","request_limit":1}`), nil, nil); err == nil || !strings.Contains(err.Error(), "must bind") {
		t.Fatalf("bindings error = %v", err)
	}
}

func TestProviderPublicOptionsRedactsCredentials(t *testing.T) {
	provider := provider{}
	raw := json.RawMessage(`{"api_url":"http://x","send_token":"send-secret","receive_secret":"receive-secret","webhook_addr":"127.0.0.1:18090","webhook_path":"/onebot","request_limit":1}`)
	public, err := provider.PublicOptions(nil, raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "send-secret") || strings.Contains(text, "receive-secret") || !strings.Contains(text, `"send_token_configured":true`) || !strings.Contains(text, `"receive_secret_configured":true`) {
		t.Fatalf("public options = %s", text)
	}
}

func TestProviderBuildDoesNotStartAdapter(t *testing.T) {
	provider := provider{}
	raw := json.RawMessage(`{"api_url":"http://x","webhook_addr":"127.0.0.1:0","webhook_path":"/onebot","request_limit":1}`)
	adapter, err := provider.Build(context.Background(), platform.ConnectorBuildRequest{ConnectorID: "qq", ConnectorOptions: raw, Bindings: []platform.ConnectorBinding{{ConversationID: "support", Target: "10001"}}})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.ID() != "qq" {
		t.Fatalf("adapter id = %q", adapter.ID())
	}
}
