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
	"github.com/soleapi/forgebot/internal/platform"
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
	if dm := cfg.Runtime.Decision.DM; dm == nil || dm.Enabled || dm.Protocol == "" || dm.Endpoint == "" || dm.Model == "" || dm.APIKeyRef == "" || dm.MinConfidence == nil {
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
		if len(status.Modules) != 10 || status.Modules[0].ID != echo.ID || !status.Modules[0].Enabled || status.Modules[1].Enabled == disabled {
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

func TestExampleConnectorsValidateWhenEnabled(t *testing.T) {
	for _, tc := range []struct{ kind, module, target string }{
		{"onebot", "native.onebot", "10001"},
		{"qqbot", "native.qqbot", "group-openid"},
		{"telegram", "native.telegram", "-1001234567890"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			cfg, _, err := config.Load(config.LoadOptions{RuntimePath: filepath.Join("..", "..", "configs", "runtime.example.json"), LookupEnv: func(string) string { return "" }})
			if err != nil {
				t.Fatal(err)
			}
			on := true
			entry := cfg.Runtime.Modules[tc.module]
			entry.Enabled = &on
			cfg.Runtime.Modules[tc.module] = entry
			var connector config.ConnectorConfig
			for index := range cfg.Runtime.Platforms.Connectors {
				if cfg.Runtime.Platforms.Connectors[index].Type == tc.kind {
					cfg.Runtime.Platforms.Connectors[index].Enabled = true
					connector = cfg.Runtime.Platforms.Connectors[index]
				}
			}
			cfg.Runtime.Platforms.Groups = append(cfg.Runtime.Platforms.Groups, config.GroupBindingConfig{ID: "support", ConnectorID: connector.ID, Target: tc.target})
			registry, err := NativeModules()
			if err != nil {
				t.Fatal(err)
			}
			if err := registry.ValidateRuntime(cfg.Runtime.Modules, cfg.Runtime.Platforms); err != nil {
				t.Fatal(err)
			}
			providers, err := registry.BuildConnectorProviders(context.Background(), cfg.Runtime.Modules, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer providers.Close(context.Background())
			registration, ok := providers.ConnectorProvider(tc.kind)
			if !ok {
				t.Fatal("example provider missing", tc.kind)
			}
			if err := registration.Provider.ValidateOptions(registration.ModuleOptions, connector.Options, []platform.ConnectorBinding{{ConversationID: "support", Target: tc.target}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// 从唯一完整示例启用 OneBot 并添加业务/运维群，验证群功能绑定链路。
func TestOneBotExampleBindsGroupsEndToEnd(t *testing.T) {
	cfg, _, err := config.Load(config.LoadOptions{
		Path:        filepath.Join("..", "..", "configs", "forgebot.example.yaml"),
		RuntimePath: filepath.Join("..", "..", "configs", "runtime.example.json"),
		LookupEnv:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatal("OneBot example no longer loads", err)
	}
	on := true
	for index := range cfg.Runtime.Platforms.Connectors {
		if cfg.Runtime.Platforms.Connectors[index].Type == "onebot" {
			cfg.Runtime.Platforms.Connectors[index].Enabled = true
		}
	}
	cfg.Runtime.Modules["native.onebot"] = config.ModuleConfig{Enabled: &on, Options: cfg.Runtime.Modules["native.onebot"].Options}
	cfg.Runtime.Modules["native.join_review"] = config.ModuleConfig{Enabled: &on, Options: json.RawMessage(`{"regex":"^.{4,}$","model_enabled":false,"groups":{"support":{"enabled":true}}}`)}
	cfg.Runtime.Platforms.Groups = append(cfg.Runtime.Platforms.Groups,
		config.GroupBindingConfig{ID: "support", ConnectorID: "qq-main", Target: "10001"},
		config.GroupBindingConfig{ID: "ops", ConnectorID: "qq-main", Target: "10002"})
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
		"native.welcome":              `{"groups":{"ghost":{"enabled":true,"message":"hi"}}}`,
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
