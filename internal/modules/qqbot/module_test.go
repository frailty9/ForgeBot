package qqbot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/platform"
)

func optionsJSON() json.RawMessage {
	return json.RawMessage(`{"app_id":"app","secret_ref":"qq/private","webhook_addr":"127.0.0.1:0","webhook_path":"/qq"}`)
}
func TestProviderStrictOptionsRedactionAndDeclaredCredentials(t *testing.T) {
	p := provider{}
	raw := optionsJSON()
	bindings := []platform.ConnectorBinding{{ConversationID: "support", Target: "group-openid"}}
	if err := p.ValidateOptions(nil, raw, bindings); err != nil {
		t.Fatal(err)
	}
	refs, err := p.CredentialRefs(nil, raw)
	if err != nil || len(refs) != 1 || refs[0] != "qq/private" {
		t.Fatal(refs, err)
	}
	public, err := p.PublicOptions(nil, raw)
	encoded, _ := json.Marshal(public)
	if err != nil || strings.Contains(string(encoded), "qq/private") {
		t.Fatal(string(encoded), err)
	}
	for _, bad := range []string{`{}`, `null`, strings.Replace(string(raw), `"app_id":"app"`, `"app_id":"app","typo":1`, 1), strings.Replace(string(raw), `"/qq"`, `"relative"`, 1)} {
		if p.ValidateOptions(nil, json.RawMessage(bad), bindings) == nil {
			t.Fatal("invalid options accepted", bad)
		}
	}
	if _, err := p.Build(context.Background(), platform.ConnectorBuildRequest{ConnectorID: "qq", ConnectorOptions: raw, Bindings: bindings}); err == nil {
		t.Fatal("missing declared credential accepted")
	}
	adapter, err := p.Build(context.Background(), platform.ConnectorBuildRequest{ConnectorID: "qq", ConnectorOptions: raw, Bindings: bindings, Credentials: map[string]string{"qq/private": "test-secret"}})
	if err != nil || adapter.ID() != "qq" {
		t.Fatal(adapter, err)
	}
	if (Factory{}).Descriptor().DefaultEnabled {
		t.Fatal("QQ provider enabled by default")
	}
}
