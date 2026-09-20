package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/echo"
	"github.com/soleapi/forgebot/internal/modules/onebot"
	"github.com/soleapi/forgebot/internal/platform"
)

func TestBuilderBuildsConnectorSetFromModuleProvider(t *testing.T) {
	registry := modules.NewRegistry()
	if err := registry.Register(echo.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	set, err := registry.Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	cfg := config.PlatformConfig{
		Connectors: []config.ConnectorConfig{{ID: "echo-main", Type: "echo", Enabled: true}},
		Groups:     []config.GroupBindingConfig{{ID: "local", ConnectorID: "echo-main", Target: "target"}},
	}
	connectors, err := (Builder{}).buildConnectors(context.Background(), cfg, set)
	if err != nil {
		t.Fatal(err)
	}
	if statuses := connectors.Status(); len(statuses) != 1 || statuses[0].ID != "echo-main" || statuses[0].State != platform.ConnectorStopped {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestBuilderRejectsMissingEnabledConnectorProvider(t *testing.T) {
	cfg := config.PlatformConfig{Connectors: []config.ConnectorConfig{{ID: "missing", Type: "missing", Enabled: true}}}
	if _, err := (Builder{}).buildConnectors(context.Background(), cfg, nil); err == nil {
		t.Fatal("missing provider accepted")
	}
}

func TestManagerValidatesProviderOptionsWithoutStartingConnector(t *testing.T) {
	registry := modules.NewRegistry()
	if err := registry.Register(echo.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(onebot.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	snapshot := config.DefaultSnapshot()
	snapshot.Runtime.Modules = make(map[string]config.ModuleConfig)
	manager, err := NewManager(context.Background(), snapshot, Builder{Modules: registry})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	enabled := true
	invalid := snapshot.Runtime.Clone()
	invalid.Modules[onebot.ID] = config.ModuleConfig{Enabled: &enabled}
	invalid.Platforms = config.PlatformConfig{Connectors: []config.ConnectorConfig{{ID: "qq-main", Type: "onebot", Enabled: true, Options: []byte(`{"api_url":"http://x"}`)}}, Groups: []config.GroupBindingConfig{{ID: "support", ConnectorID: "qq-main", Target: "10001"}}}
	if err := manager.ValidateConfiguration(invalid); err == nil || !strings.Contains(err.Error(), "webhook_addr is required") {
		t.Fatalf("invalid options error = %v", err)
	}
	missing := invalid.Clone()
	missing.Modules[onebot.ID] = config.ModuleConfig{Enabled: &enabled}
	missing.Platforms.Connectors[0].Type = "missing"
	if err := manager.ValidateConfiguration(missing); err == nil || !strings.Contains(err.Error(), `provider "missing" is unavailable`) {
		t.Fatalf("missing provider error = %v", err)
	}
}

func TestManagerPublicPlatformsRedactsProviderOptions(t *testing.T) {
	registry := modules.NewRegistry()
	if err := registry.Register(echo.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(onebot.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	snapshot := config.DefaultSnapshot()
	snapshot.Runtime.Modules = make(map[string]config.ModuleConfig)
	enabled := true
	snapshot.Runtime.Modules[onebot.ID] = config.ModuleConfig{Enabled: &enabled}
	snapshot.Runtime.Platforms = config.PlatformConfig{Connectors: []config.ConnectorConfig{
		{ID: "qq-main", Type: "onebot", Enabled: true, Options: []byte(`{"api_url":"http://x","send_token":"send-secret","receive_secret":"receive-secret","webhook_addr":"127.0.0.1:0","webhook_path":"/onebot","request_limit":1}`)},
		{ID: "future", Type: "future", Enabled: false, Options: []byte(`{"secret":"must-not-leak"}`)},
	}, Groups: []config.GroupBindingConfig{{ID: "support", ConnectorID: "qq-main", Target: "10001"}}}
	manager, err := NewManager(context.Background(), snapshot, Builder{Modules: registry})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	public, err := manager.PublicPlatforms(context.Background(), snapshot.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "send-secret") || strings.Contains(text, "receive-secret") || strings.Contains(text, "must-not-leak") || !strings.Contains(text, `"send_token_configured":true`) || !strings.Contains(text, `"provider_available":false`) {
		t.Fatalf("public platforms = %s", text)
	}
}
