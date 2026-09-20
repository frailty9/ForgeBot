package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/echo"
	"github.com/soleapi/forgebot/internal/modules/onebot"
	"github.com/soleapi/forgebot/internal/platform"
)

type testProviderModuleFactory struct{ counters *testProviderCounters }
type testProviderModuleInstance struct{ counters *testProviderCounters }
type testProviderCounters struct {
	mu     sync.Mutex
	builds int
	starts int
	stops  int
}
type testProviderAdapter struct {
	id       string
	counters *testProviderCounters
	errors   chan error
}
type testProviderSender struct{}
type testPlainModuleFactory struct{}
type testPlainModuleInstance struct{}

func (f testProviderModuleFactory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: "native.test_provider", DefaultEnabled: true, Registrations: []modules.RegistrationKind{modules.RegistrationConnectorProviders}}
}
func (testProviderModuleFactory) ValidateOptions(json.RawMessage) error { return nil }
func (testProviderModuleFactory) PublicOptions(json.RawMessage) (any, error) {
	return map[string]any{}, nil
}
func (f testProviderModuleFactory) Build(context.Context, json.RawMessage) (modules.Instance, error) {
	return testProviderModuleInstance{counters: f.counters}, nil
}
func (testProviderModuleInstance) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (testProviderModuleInstance) Close(context.Context) error        { return nil }
func (i testProviderModuleInstance) ExtendedRegistrations() modules.ExtendedRegistrations {
	return modules.ExtendedRegistrations{ConnectorProviders: []platform.ConnectorProvider{testRuntimeProvider{counters: i.counters}}}
}

type testRuntimeProvider struct{ counters *testProviderCounters }

func (testRuntimeProvider) Type() string { return "test" }
func (testRuntimeProvider) ValidateOptions(json.RawMessage, json.RawMessage, []platform.ConnectorBinding) error {
	return nil
}
func (testRuntimeProvider) PublicOptions(json.RawMessage, json.RawMessage) (any, error) {
	return map[string]any{}, nil
}
func (p testRuntimeProvider) Build(_ context.Context, request platform.ConnectorBuildRequest) (platform.Adapter, error) {
	p.counters.mu.Lock()
	p.counters.builds++
	p.counters.mu.Unlock()
	return &testProviderAdapter{id: request.ConnectorID, counters: p.counters, errors: make(chan error)}, nil
}
func (a *testProviderAdapter) ID() string                     { return a.id }
func (*testProviderAdapter) Sender() platform.ConnectorSender { return testProviderSender{} }
func (a *testProviderAdapter) Errors() <-chan error           { return a.errors }
func (a *testProviderAdapter) Start(context.Context, platform.Handler) error {
	a.counters.mu.Lock()
	a.counters.starts++
	a.counters.mu.Unlock()
	return nil
}
func (a *testProviderAdapter) Stop(context.Context) error {
	a.counters.mu.Lock()
	a.counters.stops++
	a.counters.mu.Unlock()
	return nil
}
func (testProviderSender) SendTo(context.Context, string, messaging.OutboundMessage) error {
	return nil
}
func (testPlainModuleFactory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: "native.test_plain", DefaultEnabled: true}
}
func (testPlainModuleFactory) ValidateOptions(json.RawMessage) error { return nil }
func (testPlainModuleFactory) PublicOptions(json.RawMessage) (any, error) {
	return map[string]any{}, nil
}
func (testPlainModuleFactory) Build(context.Context, json.RawMessage) (modules.Instance, error) {
	return testPlainModuleInstance{}, nil
}
func (testPlainModuleInstance) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (testPlainModuleInstance) Close(context.Context) error        { return nil }

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

func TestProviderModuleChangeRestartsConnectorGeneration(t *testing.T) {
	counters := &testProviderCounters{}
	registry := modules.NewRegistry()
	if err := registry.Register(testProviderModuleFactory{counters: counters}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	snapshot := config.DefaultSnapshot()
	snapshot.Runtime.Modules = map[string]config.ModuleConfig{"native.test_provider": {Options: json.RawMessage(`{"timeout":"1s"}`)}}
	snapshot.Runtime.Platforms = config.PlatformConfig{Connectors: []config.ConnectorConfig{{ID: "test-main", Type: "test", Enabled: true}}, Groups: []config.GroupBindingConfig{{ID: "local", ConnectorID: "test-main", Target: "target"}}}
	manager, err := NewManager(context.Background(), snapshot, Builder{Modules: registry})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	next := snapshot.Clone()
	next.Version = 2
	next.Runtime.Modules["native.test_provider"] = config.ModuleConfig{Options: json.RawMessage(`{"timeout":"2s"}`)}
	result, err := manager.Apply(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	counters.mu.Lock()
	builds, starts, stops := counters.builds, counters.starts, counters.stops
	counters.mu.Unlock()
	if result.Action != ActionRestartConnectors || builds != 2 || starts != 2 || stops != 1 {
		t.Fatalf("result=%+v builds=%d starts=%d stops=%d", result, builds, starts, stops)
	}
}

func TestPlainModuleChangeKeepsConnectorGeneration(t *testing.T) {
	counters := &testProviderCounters{}
	registry := modules.NewRegistry()
	if err := registry.Register(testProviderModuleFactory{counters: counters}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(testPlainModuleFactory{}, hooks.Native, 1, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	snapshot := config.DefaultSnapshot()
	snapshot.Runtime.Modules = map[string]config.ModuleConfig{
		"native.test_provider": {}, "native.test_plain": {Options: json.RawMessage(`{"value":1}`)},
	}
	snapshot.Runtime.Platforms = config.PlatformConfig{Connectors: []config.ConnectorConfig{{ID: "test-main", Type: "test", Enabled: true}}, Groups: []config.GroupBindingConfig{{ID: "local", ConnectorID: "test-main", Target: "target"}}}
	manager, err := NewManager(context.Background(), snapshot, Builder{Modules: registry})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	next := snapshot.Clone()
	next.Version = 2
	next.Runtime.Modules["native.test_plain"] = config.ModuleConfig{Options: json.RawMessage(`{"value":2}`)}
	result, err := manager.Apply(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	counters.mu.Lock()
	builds, starts, stops := counters.builds, counters.starts, counters.stops
	counters.mu.Unlock()
	if result.Action != ActionReplaceModules || builds != 1 || starts != 1 || stops != 0 {
		t.Fatalf("result=%+v builds=%d starts=%d stops=%d", result, builds, starts, stops)
	}
}
