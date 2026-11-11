package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/modules/qqbot"
	"github.com/soleapi/forgebot/internal/modules/replyexperience"
	"github.com/soleapi/forgebot/internal/modules/telegram"
)

func TestCapabilitiesReflectConfigurationWithoutLeakingSecrets(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Runtime.Decision.DM = &config.DecisionModelConfig{Enabled: true, Protocol: "jev", APIKeyRef: "decision/private-key-reference"}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry, Secrets: configuredSecrets(t, "answer-private-value"), ClientFactory: (&recordingFactory{}).New, Modules: moduleRegistry(t, replyexperience.Factory{})})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	catalog := manager.Capabilities(context.Background())
	if catalog.Version != snapshot.Version {
		t.Fatal(catalog)
	}
	found := false
	for _, entry := range catalog.Entries {
		if entry.ID == "decision" {
			found = true
			if entry.Configured || !entry.Loaded || entry.Health != "not_probed" {
				t.Fatal("missing DM credential marked healthy", entry)
			}
		}
	}
	if !found {
		t.Fatal("decision capability missing")
	}
	data, _ := json.Marshal(catalog)
	for _, secret := range []string{"answer-private-value", "decision/private-key-reference"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("catalog leaks secret")
		}
	}
}

func TestTelegramCatalogDeclaresOnlyImplementedCapabilities(t *testing.T) {
	snapshot := testSnapshot()
	on := true
	snapshot.Runtime.Modules = map[string]config.ModuleConfig{telegram.ID: {Enabled: &on}}
	snapshot.Runtime.Platforms.Connectors[0].Type = "telegram"
	snapshot.Runtime.Platforms.Connectors[0].Options = json.RawMessage(`{"token_ref":"telegram/private","mode":"long_polling"}`)
	snapshot.Runtime.Platforms.Groups[0].Target = "-100"
	store := configuredSecrets(t, "answer")
	candidate, err := store.Stage(context.Background(), "telegram/private", "123:private-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Activate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(context.Background(), snapshot, Builder{Modules: moduleRegistry(t, telegram.Factory{}), Secrets: store})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	catalog := manager.Capabilities(context.Background())
	found := false
	for _, entry := range catalog.Entries {
		if entry.Kind == "group" {
			found = true
			support := entry.GroupSupport
			if support == nil || !support.TextMessages || !support.MessageRecall || support.PassiveRepliesOnly || support.OutboundSegments || !support.MemberEvents || support.JoinEvents || support.JoinReview || support.Permission != "unknown" {
				t.Fatal(entry)
			}
		}
	}
	data, _ := json.Marshal(catalog)
	if !found || strings.Contains(string(data), "123:private-token") || strings.Contains(string(data), "telegram/private") {
		t.Fatal("catalog missing or leaking private settings")
	}
}

func TestQQCapabilitiesDeclarePassiveTextRecallAndMissingEvents(t *testing.T) {
	snapshot := testSnapshot()
	on := true
	snapshot.Runtime.Modules = map[string]config.ModuleConfig{qqbot.ID: {Enabled: &on}}
	snapshot.Runtime.Platforms.Connectors[0].Type = "qqbot"
	snapshot.Runtime.Platforms.Connectors[0].Options = json.RawMessage(`{"app_id":"app","secret_ref":"qq/key","webhook_addr":"127.0.0.1:0","webhook_path":"/qq"}`)
	store := configuredSecrets(t, "answer")
	candidate, err := store.Stage(context.Background(), "qq/key", "app-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Activate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(context.Background(), snapshot, Builder{Modules: moduleRegistry(t, qqbot.Factory{}), Secrets: store})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	found := false
	for _, entry := range manager.Capabilities(context.Background()).Entries {
		if entry.Kind == "group" {
			found = true
			s := entry.GroupSupport
			if s == nil || !s.TextMessages || !s.PassiveRepliesOnly || !s.MessageRecall || s.JoinReview || s.MemberEvents || s.JoinEvents || s.OutboundSegments || s.Permission != "unknown" {
				t.Fatal(entry)
			}
		}
	}
	if !found {
		t.Fatal("group capability missing")
	}
}
