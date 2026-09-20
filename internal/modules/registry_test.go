package modules_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/replyexperience"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/tooling"
)

type factory struct {
	id       string
	stages   []hooks.Stage
	instance *instance
	fail     bool
	builds   int
}

func (f *factory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: f.id, DefaultEnabled: true, Hooks: f.stages}
}
func (*factory) ValidateOptions(json.RawMessage) error      { return nil }
func (*factory) PublicOptions(json.RawMessage) (any, error) { return nil, nil }
func (f *factory) Build(context.Context, json.RawMessage) (modules.Instance, error) {
	f.builds++
	if f.fail {
		return nil, errors.New("build failed")
	}
	return f.instance, nil
}

type instance struct {
	id            string
	closes        *[]string
	registrations hooks.Registrations
}

func (i *instance) Registrations() hooks.Registrations { return i.registrations }
func (i *instance) Close(context.Context) error        { *i.closes = append(*i.closes, i.id); return nil }

func TestRegistryAtomicBuildAndReverseClose(t *testing.T) {
	var closes []string
	registry := modules.NewRegistry()
	for _, id := range []string{"a", "b"} {
		if err := registry.Register(&factory{id: id, instance: &instance{id: id, closes: &closes}}, hooks.Native, 0, hooks.Continue); err != nil {
			t.Fatal(err)
		}
	}
	bad := &factory{id: "c", fail: true}
	if err := registry.Register(bad, hooks.External, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Build(context.Background(), nil, nil); err == nil {
		t.Fatal("partial build succeeded")
	}
	if !reflect.DeepEqual(closes, []string{"b", "a"}) {
		t.Fatal(closes)
	}
	disabled := false
	closes = nil
	set, err := registry.Build(context.Background(), map[string]config.ModuleConfig{"c": {Enabled: &disabled}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	set.Mark("active")
	status := set.Status()
	if status[2].State != "disabled" {
		t.Fatal(status)
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = set.Close(context.Background())
	if !reflect.DeepEqual(closes, []string{"b", "a"}) {
		t.Fatal(closes)
	}
}

func TestRegistryRejectsUnsupportedAndUndeclaredHooks(t *testing.T) {
	registry := modules.NewRegistry()
	if err := registry.Register(&factory{id: "jobs", stages: []hooks.Stage{"job"}}, hooks.Native, 0, hooks.Continue); err == nil {
		t.Fatal("unsupported capability accepted")
	}
	var closes []string
	native := &factory{id: "bad", instance: &instance{id: "bad", closes: &closes, registrations: hooks.Registrations{AgentInput: []hooks.Registration[hooks.Input, hooks.InputResult]{{ID: "input", Handle: func(context.Context, hooks.Info, hooks.Input) (hooks.InputResult, error) {
		return hooks.InputResult{}, nil
	}}}}}}
	if err := registry.Register(native, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Build(context.Background(), nil, nil); err == nil {
		t.Fatal("undeclared hook accepted")
	}
	if len(closes) != 1 {
		t.Fatal(closes)
	}
}

func TestReplyModuleWholeSwitchDefaultsAndValidation(t *testing.T) {
	registry := modules.NewRegistry()
	if err := registry.Register(replyexperience.Factory{}, hooks.Native, 100, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	set, err := registry.Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	if len(set.Status()) != 1 || len(set.Status()[0].Hooks) != 2 {
		t.Fatal(set.Status())
	}
	for _, test := range []struct {
		input hooks.Input
		want  string
	}{{hooks.Input{Mentioned: true}, replyexperience.EmptyMentionPrompt}, {hooks.Input{Images: 1}, replyexperience.ImageOnlyPrompt}, {hooks.Input{Text: "hi", Images: 1}, ""}} {
		result, err := set.Chain().Prepare(context.Background(), hooks.Info{}, test.input)
		if err != nil {
			t.Fatal(err)
		}
		if test.want == "" {
			if result.Text != nil {
				t.Fatal("normal text changed")
			}
		} else if result.Text == nil || *result.Text != test.want {
			t.Fatal(result)
		}
	}
	disabled := false
	cfg := map[string]config.ModuleConfig{replyexperience.ID: {Enabled: &disabled, Options: json.RawMessage(`{"empty_mention_prompt":"hello"}`)}}
	off, err := registry.Build(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer off.Close(context.Background())
	if off.Status()[0].Enabled || len(off.Status()[0].Hooks) != 0 {
		t.Fatal("partial disable")
	}
	result, err := off.Chain().Prepare(context.Background(), hooks.Info{}, hooks.Input{Mentioned: true})
	if err != nil || result.Text != nil {
		t.Fatal(result, err)
	}
	tool, _ := off.Chain().Tool(context.Background(), hooks.Info{}, hooks.ToolInput{Name: "search_docs"})
	if tool.Progress != nil {
		t.Fatal("progress survived disable")
	}
	public, err := registry.Public(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if public[replyexperience.ID].(map[string]any)["enabled"] != false {
		t.Fatal(public)
	}
	for _, raw := range []string{`{"unknown":1}`, `{"empty_mention_prompt":" "}`, `{"empty_mention_prompt":null}`} {
		if err := registry.Validate(map[string]config.ModuleConfig{replyexperience.ID: {Enabled: &disabled, Options: json.RawMessage(raw)}}); err == nil {
			t.Fatal("invalid options accepted", raw)
		}
	}
	if err := registry.Validate(map[string]config.ModuleConfig{"missing": {Enabled: &disabled}}); err == nil {
		t.Fatal("unknown id accepted")
	}
}

func TestValidationDoesNotBuild(t *testing.T) {
	registry := modules.NewRegistry()
	plugin := &factory{id: "custom"}
	if err := registry.Register(plugin, hooks.External, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate(nil); err != nil {
		t.Fatal(err)
	}
	if plugin.builds != 0 {
		t.Fatal("validation started module")
	}
}

type extendedFactory struct {
	id            string
	provides      capability.ID
	requires      capability.ID
	registrations []modules.RegistrationKind
	instance      modules.Instance
}

func (f *extendedFactory) Descriptor() modules.Descriptor {
	registrations := f.registrations
	if registrations == nil {
		registrations = []modules.RegistrationKind{modules.RegistrationTools}
	}
	descriptor := modules.Descriptor{ID: f.id, DefaultEnabled: true, Registrations: registrations}
	if f.provides != "" {
		descriptor.Provides = []capability.ID{f.provides}
	}
	if f.requires != "" {
		descriptor.Requires = []capability.ID{f.requires}
	}
	return descriptor
}
func (*extendedFactory) ValidateOptions(json.RawMessage) error      { return nil }
func (*extendedFactory) PublicOptions(json.RawMessage) (any, error) { return nil, nil }
func (f *extendedFactory) Build(context.Context, json.RawMessage) (modules.Instance, error) {
	return f.instance, nil
}
func (f *extendedFactory) BuildWithContext(_ context.Context, build modules.BuildContext, _ json.RawMessage) (modules.Instance, error) {
	if f.requires != "" && !build.Capabilities.Has(f.requires) {
		return nil, errors.New("required capability was not prepared")
	}
	return f.instance, nil
}

type extendedInstance struct {
	capabilityID capability.ID
	tool         tooling.Tool
	providers    []platform.ConnectorProvider
	started      *int
	retired      *int
}

func (i *extendedInstance) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (i *extendedInstance) Close(context.Context) error        { return nil }
func (i *extendedInstance) Start(context.Context) {
	if i.started != nil {
		*i.started++
	}
}
func (i *extendedInstance) Retire() {
	if i.retired != nil {
		*i.retired++
	}
}
func (i *extendedInstance) ExtendedRegistrations() modules.ExtendedRegistrations {
	registrations := modules.ExtendedRegistrations{ConnectorProviders: i.providers}
	if i.tool != nil {
		registrations.Tools = []modules.ToolRegistration{{ID: "tool", Tool: i.tool, MaxCalls: 1}}
	}
	if i.capabilityID != "" {
		registrations.Capabilities = []modules.CapabilityRegistration{{ID: i.capabilityID, Value: struct{}{}}}
	}
	return registrations
}

type testConnectorProvider struct{ typeName string }

func (p testConnectorProvider) Type() string { return p.typeName }
func (testConnectorProvider) ValidateOptions(json.RawMessage, json.RawMessage, []platform.ConnectorBinding) error {
	return nil
}
func (testConnectorProvider) PublicOptions(json.RawMessage, json.RawMessage) (any, error) {
	return map[string]any{}, nil
}
func (testConnectorProvider) Build(context.Context, platform.ConnectorBuildRequest) (platform.Adapter, error) {
	return nil, nil
}

type testTool struct{ name string }

func (t testTool) Definition() tooling.Definition { return tooling.Definition{Name: t.name} }
func (testTool) Execute(context.Context, tooling.Call) tooling.Result {
	return tooling.Result{OK: true}
}

func TestRegistryBuildsCapabilityDependenciesAndCollectsTools(t *testing.T) {
	provided := capability.ID("example.provider.v1")
	required := capability.ID("example.consumer.v1")
	registry := modules.NewRegistry()
	provider := &extendedFactory{id: "provider", provides: provided, instance: &extendedInstance{capabilityID: provided, tool: testTool{name: "provider_tool"}}}
	consumer := &extendedFactory{id: "consumer", requires: provided, provides: required, instance: &extendedInstance{capabilityID: required, tool: testTool{name: "consumer_tool"}}}
	if err := registry.Register(consumer, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(provider, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	set, err := registry.Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	if !set.Capabilities().Has(required) || len(set.Tools()) != 2 {
		t.Fatalf("capabilities/tools = %v/%d", set.Capabilities().Has(required), len(set.Tools()))
	}
}

func TestRegistryRejectsDuplicateTools(t *testing.T) {
	registry := modules.NewRegistry()
	first := &extendedFactory{id: "first", provides: "example.first.v1", instance: &extendedInstance{capabilityID: "example.first.v1", tool: testTool{name: "duplicate"}}}
	second := &extendedFactory{id: "second", provides: "example.second.v1", instance: &extendedInstance{capabilityID: "example.second.v1", tool: testTool{name: "duplicate"}}}
	if err := registry.Register(first, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(second, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Build(context.Background(), nil, nil); err == nil {
		t.Fatal("duplicate tool accepted")
	}
}

func TestRegistryLifecycleStartsAndRetiresInstances(t *testing.T) {
	started, retired := 0, 0
	registry := modules.NewRegistry()
	item := &extendedInstance{capabilityID: "example.lifecycle.v1", tool: testTool{name: "lifecycle_tool"}, started: &started, retired: &retired}
	factory := &extendedFactory{id: "lifecycle", provides: "example.lifecycle.v1", instance: item}
	if err := registry.Register(factory, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	set, err := registry.Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if started != 0 || retired != 0 {
		t.Fatalf("prepared lifecycle ran early: %d/%d", started, retired)
	}
	set.Start(context.Background())
	if started != 1 {
		t.Fatalf("started = %d", started)
	}
	set.Retire()
	if retired != 1 {
		t.Fatalf("retired = %d", retired)
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if retired != 1 {
		t.Fatalf("close repeated retire = %d", retired)
	}
}

func TestRegistryCollectsConnectorProvidersAndCopiesOptions(t *testing.T) {
	registry := modules.NewRegistry()
	provider := testConnectorProvider{typeName: "echo"}
	factory := &extendedFactory{
		id: "native.echo", registrations: []modules.RegistrationKind{modules.RegistrationConnectorProviders},
		instance: &extendedInstance{providers: []platform.ConnectorProvider{provider}},
	}
	if err := registry.Register(factory, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	options := json.RawMessage(`{"label":"original"}`)
	set, err := registry.Build(context.Background(), map[string]config.ModuleConfig{"native.echo": {Options: options}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	if got := set.ConnectorProviderTypes(); !reflect.DeepEqual(got, []string{"echo"}) {
		t.Fatalf("provider types = %v", got)
	}
	registration, ok := set.ConnectorProvider("echo")
	if !ok || registration.ModuleID != "native.echo" || registration.Provider != provider {
		t.Fatalf("provider registration = %#v, %v", registration, ok)
	}
	options[10] = 'X'
	if string(registration.ModuleOptions) != `{"label":"original"}` {
		t.Fatalf("module options aliased input: %s", registration.ModuleOptions)
	}
	registration.ModuleOptions[10] = 'Y'
	again, ok := set.ConnectorProvider("echo")
	if !ok || string(again.ModuleOptions) != `{"label":"original"}` {
		t.Fatalf("module options aliased caller: %s", again.ModuleOptions)
	}
}

func TestRegistryRejectsInvalidAndDuplicateConnectorProviders(t *testing.T) {
	t.Run("undeclared", func(t *testing.T) {
		registry := modules.NewRegistry()
		factory := &extendedFactory{id: "undeclared", instance: &extendedInstance{providers: []platform.ConnectorProvider{testConnectorProvider{typeName: "echo"}}}}
		if err := registry.Register(factory, hooks.Native, 0, hooks.Continue); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.Build(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "undeclared connector provider") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("invalid type", func(t *testing.T) {
		registry := modules.NewRegistry()
		factory := &extendedFactory{id: "invalid", registrations: []modules.RegistrationKind{modules.RegistrationConnectorProviders}, instance: &extendedInstance{providers: []platform.ConnectorProvider{testConnectorProvider{typeName: "invalid type"}}}}
		if err := registry.Register(factory, hooks.Native, 0, hooks.Continue); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.Build(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "invalid connector provider") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		registry := modules.NewRegistry()
		for _, id := range []string{"first", "second"} {
			factory := &extendedFactory{id: id, registrations: []modules.RegistrationKind{modules.RegistrationConnectorProviders}, instance: &extendedInstance{providers: []platform.ConnectorProvider{testConnectorProvider{typeName: "echo"}}}}
			if err := registry.Register(factory, hooks.Native, 0, hooks.Continue); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := registry.Build(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "both first and second") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestRegistryConnectorProviderChangeDetection(t *testing.T) {
	registry := modules.NewRegistry()
	providerFactory := &extendedFactory{id: "native.echo", registrations: []modules.RegistrationKind{modules.RegistrationConnectorProviders}, instance: &extendedInstance{}}
	plainFactory := &extendedFactory{id: "native.plain", instance: &extendedInstance{tool: testTool{name: "plain"}}}
	if err := registry.Register(providerFactory, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(plainFactory, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	current := map[string]config.ModuleConfig{"native.echo": {Options: json.RawMessage(`{"v":1}`)}}
	if registry.ConnectorProvidersChanged(current, current) {
		t.Fatal("unchanged configuration reported")
	}
	next := map[string]config.ModuleConfig{"native.echo": {Options: json.RawMessage(`{"v":2}`)}}
	if !registry.ConnectorProvidersChanged(current, next) {
		t.Fatal("provider options change not reported")
	}
	disabled := false
	next["native.echo"] = config.ModuleConfig{Enabled: &disabled, Options: json.RawMessage(`{"v":1}`)}
	if !registry.ConnectorProvidersChanged(current, next) {
		t.Fatal("provider enablement change not reported")
	}
	plainNext := map[string]config.ModuleConfig{"native.echo": current["native.echo"], "native.plain": {Options: json.RawMessage(`{"v":2}`)}}
	if registry.ConnectorProvidersChanged(current, plainNext) {
		t.Fatal("plain module change reported as provider change")
	}
}

func BenchmarkNativeReplyChain(b *testing.B) {
	registry := modules.NewRegistry()
	_ = registry.Register(replyexperience.Factory{}, hooks.Native, 100, hooks.Continue)
	set, err := registry.Build(context.Background(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		b.Fatal(err)
	}
	defer set.Close(context.Background())
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = set.Chain().Prepare(ctx, hooks.Info{}, hooks.Input{Mentioned: true})
	}
}
