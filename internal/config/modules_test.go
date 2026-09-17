package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestModuleConfigStrictAndBounded(t *testing.T) {
	var omitted, disabled ModuleConfig
	if err := json.Unmarshal([]byte(`{}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"enabled":false,"options":{}}`), &disabled); err != nil {
		t.Fatal(err)
	}
	if omitted.Enabled != nil || disabled.Enabled == nil || *disabled.Enabled {
		t.Fatal("lost absence/false distinction")
	}
	if err := json.Unmarshal([]byte(`{"priority":1}`), &omitted); err == nil {
		t.Fatal("unknown field accepted")
	}
	for _, entries := range []map[string]ModuleConfig{
		{"bad id": {}}, {"native.test": {Options: json.RawMessage(`null`)}}, {"native.test": {Options: json.RawMessage(`[]`)}}, {"native.test": {Options: json.RawMessage(strings.Repeat(" ", MaxModuleOptionsBytes+1))}},
	} {
		if err := ValidateModules(entries); err == nil {
			t.Fatal("invalid module config accepted")
		}
	}
	entries := map[string]ModuleConfig{}
	for index := 0; index < MaxModules+1; index++ {
		entries["m"+strings.Repeat("x", index)] = ModuleConfig{}
	}
	if err := ValidateModules(entries); err == nil {
		t.Fatal("unbounded module count")
	}
}

func TestRuntimeClonePreservesAndIsolatesModuleOptions(t *testing.T) {
	enabled := true
	original := DefaultRuntime()
	original.Modules = map[string]ModuleConfig{"native.test": {Enabled: &enabled, Options: json.RawMessage(`{"value":"ok"}`)}}
	cloned := original.Clone()
	*cloned.Modules["native.test"].Enabled = false
	cloned.Modules["native.test"].Options[0] = '['
	delete(cloned.Modules, "native.test")
	if !*original.Modules["native.test"].Enabled || original.Modules["native.test"].Options[0] != '{' {
		t.Fatal("clone aliases original")
	}
	if DefaultRuntime().Clone().Modules != nil {
		t.Fatal("nil normalized into empty map")
	}
}
