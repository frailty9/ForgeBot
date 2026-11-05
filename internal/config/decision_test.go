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

func TestDisabledDecisionBlocksRoundTripAndStillValidate(t *testing.T) {
	var cfg RuntimeConfig
	if err := decodeStrictJSON([]byte(`{"decision":{"dm":{"disabled":true,"provider":"jev","endpoint":"https://api.typesafe.ai","model":"jev-latest","api_key_ref":"decision/default"}}}`), &cfg); err != nil {
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
	copy.Decision.DM.Disabled = false
	if !copy.Decision.DMEnabled() || roundtrip.Decision.DMEnabled() {
		t.Fatal("cloned flags alias source")
	}
	copy.Decision.DM.Provider = "typo"
	copy.Decision.DM.Disabled = true
	if err = copy.Decision.Validate(); err == nil {
		t.Fatal("disabled DM bypassed field validation")
	}
}
