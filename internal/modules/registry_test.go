package modules_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/replyexperience"
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
	id       string
	provides capability.ID
	requires capability.ID
	instance modules.Instance
}

func (f *extendedFactory) Descriptor() modules.Descriptor {
	descriptor := modules.Descriptor{ID: f.id, DefaultEnabled: true, Registrations: []modules.RegistrationKind{modules.RegistrationTools}}
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
	return modules.ExtendedRegistrations{
		Tools:        []modules.ToolRegistration{{ID: "tool", Tool: i.tool, MaxCalls: 1}},
		Capabilities: []modules.CapabilityRegistration{{ID: i.capabilityID, Value: struct{}{}}},
	}
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
	if retired != 2 {
		t.Fatalf("close repeated retire = %d", retired)
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
