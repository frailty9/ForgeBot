package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/platform"
)

func TestProviderValidationCredentialsAndPublicOptions(t *testing.T) {
	p := provider{}
	raw := json.RawMessage(`{"token_ref":"telegram/private","mode":"long_polling","timeout":"15s"}`)
	bindings := []platform.ConnectorBinding{{ConversationID: "support", Target: "-1001234567890"}}
	if err := p.ValidateOptions(nil, raw, bindings); err != nil {
		t.Fatal(err)
	}
	refs, err := p.CredentialRefs(nil, raw)
	if err != nil || len(refs) != 1 || refs[0] != "telegram/private" {
		t.Fatal(refs, err)
	}
	public, err := p.PublicOptions(nil, raw)
	data, _ := json.Marshal(public)
	if err != nil || strings.Contains(string(data), "telegram/private") || !strings.Contains(string(data), `"token_configured":true`) {
		t.Fatal(string(data), err)
	}
	for _, invalid := range []string{
		`{}`, `null`, `{"token_ref":"key","mode":"webhook"}`,
		`{"token_ref":"key","mode":"other"}`,
		`{"token_ref":"key","mode":"webhook","webhook_url":"http://bot.example.com/tg","webhook_addr":"127.0.0.1:0","webhook_path":"/tg","webhook_secret_ref":"tg/hook"}`,
		`{"token_ref":"key","mode":"webhook","webhook_url":"https://bot.example.com:9000/tg","webhook_addr":"127.0.0.1:0","webhook_path":"/tg","webhook_secret_ref":"tg/hook"}`,
		`{"token_ref":"key","mode":"webhook","webhook_url":"https://bot.example.com/tg","webhook_addr":"127.0.0.1:0","webhook_path":"/tg","webhook_secret_ref":"key"}`,
		`{"token_ref":"key","mode":"long_polling","webhook_url":"not a url"}`,
		`{"token_ref":"key","mode":"long_polling","timeout":"0s"}`,
		`{"token_ref":"key","mode":"long_polling","token":"secret"}`,
		`{"token_ref":"../key","mode":"long_polling"}`,
		string(raw) + `{}`,
	} {
		if p.ValidateOptions(nil, json.RawMessage(invalid), bindings) == nil {
			t.Fatal("invalid options accepted", invalid)
		}
	}
	for _, invalid := range []string{"@group", "0", "01", "+42", "9223372036854775808"} {
		if p.ValidateOptions(nil, raw, []platform.ConnectorBinding{{ConversationID: "support", Target: invalid}}) == nil {
			t.Fatal("invalid chat_id accepted", invalid)
		}
	}
	if p.ValidateOptions(nil, raw, nil) == nil || (Factory{}).ValidateOptions(json.RawMessage(`{"default_token":"key"}`)) == nil {
		t.Fatal("missing bindings or module options accepted")
	}
	request := platform.ConnectorBuildRequest{ConnectorID: "telegram", ConnectorOptions: raw, Bindings: bindings}
	if _, err := p.Build(context.Background(), request); err == nil {
		t.Fatal("missing credential accepted")
	}
	request.Credentials = map[string]string{"telegram/private": "123:test-token"}
	adapter, err := p.Build(context.Background(), request)
	if err != nil || adapter.ID() != "telegram" || (Factory{}).Descriptor().DefaultEnabled {
		t.Fatal(adapter, err)
	}
}

func TestWebhookModeDeclaresOnlyItsCredentials(t *testing.T) {
	p := provider{}
	bindings := []platform.ConnectorBinding{{ConversationID: "support", Target: "-100"}}
	webhook := json.RawMessage(`{"token_ref":"tg/token","mode":"webhook","webhook_url":"https://bot.example.com/tg","webhook_addr":"127.0.0.1:0","webhook_path":"/tg","webhook_secret_ref":"tg/hook"}`)
	polling := json.RawMessage(`{"token_ref":"tg/token","mode":"long_polling","webhook_url":"https://bot.example.com/tg","webhook_addr":"127.0.0.1:0","webhook_path":"/tg","webhook_secret_ref":"tg/hook"}`)
	for raw, want := range map[string]string{string(webhook): "tg/token,tg/hook", string(polling): "tg/token"} {
		refs, err := p.CredentialRefs(nil, json.RawMessage(raw))
		if err != nil || strings.Join(refs, ",") != want {
			t.Fatal(refs, err)
		}
		if err := p.ValidateOptions(nil, json.RawMessage(raw), bindings); err != nil {
			t.Fatal(err)
		}
	}
	public, _ := p.PublicOptions(nil, webhook)
	data, _ := json.Marshal(public)
	if strings.Contains(string(data), "tg/hook") || !strings.Contains(string(data), `"webhook_secret_configured":true`) {
		t.Fatal(string(data))
	}
	request := platform.ConnectorBuildRequest{ConnectorID: "telegram", ConnectorOptions: webhook, Bindings: bindings,
		Credentials: map[string]string{"tg/token": "123:test-token", "tg/hook": "bad secret"}}
	if _, err := p.Build(context.Background(), request); err == nil {
		t.Fatal("invalid webhook secret accepted")
	}
	request.Credentials["tg/hook"] = "valid_secret-1"
	if _, err := p.Build(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}
