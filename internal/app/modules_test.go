package app

import (
	"context"
	"encoding/json"
	"path/filepath"
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
		if len(status.Modules) != 7 || status.Modules[0].ID != echo.ID || !status.Modules[0].Enabled || status.Modules[1].Enabled == disabled || status.Modules[2].Enabled || status.Modules[3].Enabled || status.Modules[4].Enabled || status.Modules[5].Enabled || status.Modules[6].Enabled {
			t.Fatal(status.Modules)
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
