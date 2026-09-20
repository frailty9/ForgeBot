package structuredsearch_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/knowledge"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/structuredsearch"
	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/tooling"
)

func buildSearchModule(t *testing.T) *modules.Set {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "auth.mdx"), []byte("# Authentication\n\nUse the API key header."), 0644); err != nil {
		t.Fatal(err)
	}
	options, _ := json.Marshal(map[string]any{"document_root": root, "max_queries_per_turn": 2})
	enabled := true
	registry := modules.NewRegistry()
	if err := registry.Register(structuredsearch.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	set, err := registry.BuildWithCapabilities(context.Background(), map[string]config.ModuleConfig{
		structuredsearch.ID: {Enabled: &enabled, Options: options},
	}, nil, capability.NewValues())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return set
}

func TestModuleRegistersToolCommandAndCapabilities(t *testing.T) {
	set := buildSearchModule(t)
	tools := set.Tools()
	if len(tools) != 1 || tools[0].Tool.Definition().Name != "search_docs" || tools[0].MaxCalls != 2 {
		t.Fatalf("tools = %+v", tools)
	}
	commands := set.Commands()
	if len(commands) != 1 || commands[0].Name != "search" {
		t.Fatalf("commands = %+v", commands)
	}
	result, err := commands[0].Handle(context.Background(), modules.CommandRequest{Name: "search", Arguments: []string{"API", "key"}})
	if err != nil || !strings.Contains(result.Reply, "auth.mdx") || !strings.Contains(result.Reply, "API key") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	values := set.Capabilities()
	if control, ok := capability.Resolve(values, searchcontract.ControlKey); !ok || control == nil {
		t.Fatal("search control capability is missing")
	}
	if factory, ok := capability.Resolve(values, knowledge.PublisherFactoryKey); !ok || factory == nil {
		t.Fatal("publisher factory capability is missing")
	}
	toolResult := set.ToolExecutor().Execute(context.Background(), tooling.Call{Name: "search_docs", Arguments: `{"query":"authentication"}`})
	if !toolResult.OK || !strings.Contains(toolResult.Payload, "auth.mdx") {
		t.Fatalf("tool result = %+v", toolResult)
	}
}

func TestModuleCommandHandlesEmptyQuery(t *testing.T) {
	set := buildSearchModule(t)
	result, err := set.Commands()[0].Handle(context.Background(), modules.CommandRequest{Name: "search"})
	if err != nil || result.Reply != "用法：/search <关键词或问题>" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestOptionsRejectUnknownRelativeAndInvalidLimits(t *testing.T) {
	factory := structuredsearch.Factory{}
	for _, raw := range []string{
		`{"unknown":true}`,
		`{"document_root":"relative"}`,
		`{"document_root":"D:/docs","default_results":9,"max_results":8}`,
		`{"document_root":"D:/docs","extensions":[".md",".md"]}`,
	} {
		if err := factory.ValidateOptions(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid options accepted", raw)
		}
	}
}

func TestDisabledModuleDoesNotBuildIndexOrRegisterTool(t *testing.T) {
	registry := modules.NewRegistry()
	if err := registry.Register(structuredsearch.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	disabled := false
	set, err := registry.Build(context.Background(), map[string]config.ModuleConfig{
		structuredsearch.ID: {Enabled: &disabled},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	if len(set.Tools()) != 0 || len(set.Commands()) != 0 {
		t.Fatal("disabled search registered runtime surfaces")
	}
}
