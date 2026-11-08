package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules/echo"
	"github.com/soleapi/forgebot/internal/modules/replyexperience"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
)

// 示例必须显式列出全部注册的原生模块，防止新增功能只能从文档中发现。
func TestExampleDeclaresEveryNativeModule(t *testing.T) {
	cfg, _, err := config.Load(config.LoadOptions{Path: filepath.Join("..", "..", "configs", "forgebot.example.yaml"), LookupEnv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NativeModules()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate(cfg.Runtime.Modules); err != nil {
		t.Fatal(err)
	}
	declared, err := registry.Public(cfg.Runtime.Modules)
	if err != nil {
		t.Fatal(err)
	}
	for id := range declared {
		entry, exists := cfg.Runtime.Modules[id]
		if !exists || entry.Enabled == nil || len(entry.Options) == 0 {
			t.Errorf("example must explicitly declare %s enabled/options", id)
			continue
		}
		if id != echo.ID && *entry.Enabled {
			t.Errorf("optional example module %s should be disabled", id)
		}
	}
	if dm := cfg.Runtime.Decision.DM; dm == nil || !dm.Disabled || dm.Provider == "" || dm.Endpoint == "" || dm.Model == "" || dm.APIKeyRef == "" || dm.MinConfidence == nil {
		t.Fatal("example must declare complete disabled DM configuration")
	}
	// 完整示例关闭的 DM 不得使构建访问供应商。
	engine, err := (runtimecfg.Builder{Modules: registry, Sender: exampleSender{}}).Build(context.Background(), cfg)
	if err != nil {
		t.Fatal("disabled example blocks runtime construction", err)
	}
	engine.Shutdown(0)
}

type exampleSender struct{}

func (exampleSender) Send(context.Context, messaging.OutboundMessage) error { return nil }

func TestAppRegistersDefaultNativeModule(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		snapshot := configuredAppSnapshot(t)
		if disabled {
			enabled := false
			snapshot.Runtime.Modules = map[string]config.ModuleConfig{replyexperience.ID: {Enabled: &enabled}}
		}
		root, err := New(snapshot, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := root.runtime.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "local", UserID: "u", MessageID: "m", Mentioned: true})
		if err != nil {
			t.Fatal(err)
		}
		if disabled && result.Verdict != "empty_input" || !disabled && !result.Accepted {
			t.Fatal(result)
		}
		_, status := root.runtime.Snapshot()
		if len(status.Modules) != 8 || status.Modules[0].ID != echo.ID || !status.Modules[0].Enabled || status.Modules[1].Enabled == disabled {
			t.Fatal(status.Modules)
		}
		for _, module := range status.Modules[2:] {
			if module.Enabled {
				t.Fatal("optional module unexpectedly enabled", status.Modules)
			}
		}
		if err := root.runtime.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAppRejectsInvalidModuleBeforeServing(t *testing.T) {
	snapshot := config.DefaultSnapshot()
	snapshot.Runtime.Modules = map[string]config.ModuleConfig{replyexperience.ID: {Options: json.RawMessage(`{"typo":true}`)}}
	if _, err := New(snapshot, nil); err == nil {
		t.Fatal("invalid module accepted")
	}
}

// QQ 完整示例展示“连接器 → 逻辑群 → 群级功能”的绑定链路，必须能直接加载并通过模块校验。
func TestOneBotExampleBindsGroupsEndToEnd(t *testing.T) {
	cfg, _, err := config.Load(config.LoadOptions{
		Path:        filepath.Join("..", "..", "configs", "forgebot.example.yaml"),
		RuntimePath: filepath.Join("..", "..", "configs", "runtime.onebot.example.json"),
		LookupEnv:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatal("OneBot example no longer loads", err)
	}
	registry, err := NativeModules()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate(cfg.Runtime.Modules); err != nil {
		t.Fatal(err)
	}
	bound := map[string]string{}
	for _, group := range cfg.Runtime.Platforms.Groups {
		bound[group.ID] = group.ConnectorID
	}
	if bound["support"] != "qq-main" || bound["ops"] != "qq-main" {
		t.Fatalf("example groups not bound to qq-main: %v", bound)
	}
	if entry := cfg.Runtime.Modules["native.join_review"]; entry.Enabled == nil || !*entry.Enabled || !strings.Contains(string(entry.Options), `"support"`) {
		t.Fatal("example should show join review enabled on the support group")
	}
	if err := registry.ValidateRuntime(cfg.Runtime.Modules, cfg.Runtime.Platforms); err != nil {
		t.Fatal("example references an unbound group", err)
	}
}

// 按逻辑群配置的模块引用未绑定的群 ID 时，运行时校验直接拒绝，避免功能静默不生效。
func TestModulesRejectUnboundGroupReferences(t *testing.T) {
	registry, err := NativeModules()
	if err != nil {
		t.Fatal(err)
	}
	platforms := config.PlatformConfig{Groups: []config.GroupBindingConfig{{ID: "local", ConnectorID: "echo", Target: "local"}}}
	for id, options := range map[string]string{
		"native.join_review":          `{"regex":".","groups":{"ghost":{"enabled":true}}}`,
		"native.welcome":              `{"groups":{"ghost":{"enabled":true,"text":"hi"}}}`,
		"native.proactive_reply":      `{"groups":{"ghost":{"enabled":true}}}`,
		"native.knowledge_reflection": `{"conversation_ids":["ghost"]}`,
	} {
		entries := map[string]config.ModuleConfig{id: {Options: json.RawMessage(options)}}
		if err := registry.ValidateRuntime(entries, platforms); err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Fatalf("%s accepted unbound group: %v", id, err)
		}
		bound := strings.ReplaceAll(options, "ghost", "local")
		if err := registry.ValidateRuntime(map[string]config.ModuleConfig{id: {Options: json.RawMessage(bound)}}, platforms); err != nil {
			t.Fatalf("%s rejected bound group: %v", id, err)
		}
	}
}
