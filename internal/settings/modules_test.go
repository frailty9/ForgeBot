package settings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
)

func TestModuleSnapshotsDoNotAliasStore(t *testing.T) {
	ctx := context.Background()
	store, err := NewJSONFileStore(writeRuntimeFile(t), config.DefaultStatic())
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	runtime := before.Runtime.Clone()
	runtime.Modules = map[string]config.ModuleConfig{"native.test": {Enabled: &enabled, Options: json.RawMessage(`{"value":"ok"}`)}}
	committed, err := store.Update(ctx, UpdateRequest{ExpectedVersion: before.Version, Runtime: runtime, Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	*runtime.Modules["native.test"].Enabled = false
	runtime.Modules["native.test"].Options[0] = '['
	*committed.Runtime.Modules["native.test"].Enabled = false
	committed.Runtime.Modules["native.test"].Options[0] = '['
	current, _ := store.Current(ctx)
	if !*current.Runtime.Modules["native.test"].Enabled || string(current.Runtime.Modules["native.test"].Options) != `{"value":"ok"}` {
		t.Fatal("snapshot alias")
	}
	historicVersion := current.Version
	next := current.Runtime.Clone()
	next.Modules = nil
	_, err = store.Update(ctx, UpdateRequest{ExpectedVersion: current.Version, Runtime: next, Source: "next"})
	if err != nil {
		t.Fatal(err)
	}
	historic, _ := store.RuntimeAt(ctx, historicVersion)
	*historic.Modules["native.test"].Enabled = false
	historic.Modules["native.test"].Options[0] = '['
	latest, _ := store.Current(ctx)
	restored, err := store.Rollback(ctx, historicVersion, latest.Version, "rollback")
	if err != nil {
		t.Fatal(err)
	}
	if !*restored.Runtime.Modules["native.test"].Enabled || string(restored.Runtime.Modules["native.test"].Options) != `{"value":"ok"}` {
		t.Fatal("history alias")
	}
}
