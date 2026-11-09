package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecisionConfigStrictnessAndSnapshotIsolation(t *testing.T) {
	for _, raw := range []string{`{"decision":{"typo":1}}`, `{"decision":{"dm":{"provider":"unknown","api_key_ref":"dm/key"}}}`, `{"decision":{"dm":{"provider":"jev","api_key_ref":"dm/key","endpoint":"https://user:pass@example.com"}}}`} {
		cfg := DefaultRuntime()
		err := decodeStrictJSON([]byte(raw), &cfg)
		if err == nil {
			err = cfg.Validate()
		}
		if err == nil {
			t.Fatal("invalid config accepted", raw)
		}
	}
	confidence := 0.6
	cfg := DefaultRuntime()
	cfg.Decision.DM = &DecisionModelConfig{Provider: "jev", APIKeyRef: "dm/private", MinConfidence: &confidence}
	copy := cfg.Clone()
	*copy.Decision.DM.MinConfidence = 0
	if *cfg.Decision.DM.MinConfidence != 0.6 {
		t.Fatal("decision clone aliases config")
	}
	data, _ := json.Marshal((Snapshot{Runtime: cfg}).Public(false))
	if strings.Contains(string(data), "dm/private") {
		t.Fatal("decision credential reference exposed")
	}
}

// DM 缺省关闭：写出 dm 对象只保留供应商参数，必须显式 enabled=true 才生效；
// 旧版的 disabled 键已移除，严格解析会拒绝。
func TestDMIsOffUnlessExplicitlyEnabled(t *testing.T) {
	const block = `"provider":"jev","endpoint":"https://api.typesafe.ai","model":"jev-latest","api_key_ref":"decision/default"`
	for raw, want := range map[string]bool{
		`{"decision":{"dm":{` + block + `}}}`:                 false,
		`{"decision":{"dm":{"enabled":false,` + block + `}}}`: false,
		`{"decision":{"dm":{"enabled":true,` + block + `}}}`:  true,
		`{"decision":{}}`: false,
	} {
		var cfg RuntimeConfig
		if err := decodeStrictJSON([]byte(raw), &cfg); err != nil {
			t.Fatal(raw, err)
		}
		if cfg.Decision.DMEnabled() != want {
			t.Fatalf("DMEnabled = %v, want %v for %s", cfg.Decision.DMEnabled(), want, raw)
		}
	}
	var legacy RuntimeConfig
	if err := decodeStrictJSON([]byte(`{"decision":{"dm":{"disabled":true,`+block+`}}}`), &legacy); err == nil {
		t.Fatal("removed disabled key accepted")
	}
}

func TestDisabledDecisionBlocksRoundTripAndStillValidate(t *testing.T) {
	var cfg RuntimeConfig
	if err := decodeStrictJSON([]byte(`{"decision":{"dm":{"enabled":false,"provider":"jev","endpoint":"https://api.typesafe.ai","model":"jev-latest","api_key_ref":"decision/default"}}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip RuntimeConfig
	if err = decodeStrictJSON(data, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if roundtrip.Decision.DMEnabled() {
		t.Fatal("disabled flags lost")
	}
	copy := roundtrip.Clone()
	copy.Decision.DM.Enabled = true
	if !copy.Decision.DMEnabled() || roundtrip.Decision.DMEnabled() {
		t.Fatal("cloned flags alias source")
	}
	copy.Decision.DM.Provider = "typo"
	copy.Decision.DM.Enabled = false
	if err = copy.Decision.Validate(); err == nil {
		t.Fatal("disabled DM bypassed field validation")
	}
}
