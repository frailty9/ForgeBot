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
	daily := true
	cfg := DefaultRuntime()
	cfg.Decision.DM = &DecisionModelConfig{Provider: "jev", APIKeyRef: "dm/private", MinConfidence: &confidence}
	cfg.AnswerPolicy.AllowDaily = &daily
	cfg.AnswerPolicy.Evidence = &EvidenceBinding{Tool: "search_docs", QueryArgument: "query", Arguments: map[string]any{"filters": map[string]any{"x": "original"}}}
	copy := cfg.Clone()
	*copy.Decision.DM.MinConfidence = 0
	*copy.AnswerPolicy.AllowDaily = false
	copy.AnswerPolicy.Evidence.Arguments["filters"].(map[string]any)["x"] = "changed"
	if *cfg.Decision.DM.MinConfidence != 0.6 || !*cfg.AnswerPolicy.AllowDaily || cfg.AnswerPolicy.Evidence.Arguments["filters"].(map[string]any)["x"] != "original" {
		t.Fatal("decision clone aliases config")
	}
	data, _ := json.Marshal((Snapshot{Runtime: cfg}).Public(false))
	if strings.Contains(string(data), "dm/private") {
		t.Fatal("decision credential reference exposed")
	}
}

func TestDisabledDecisionBlocksRoundTripAndStillValidate(t *testing.T) {
	var cfg RuntimeConfig
	if err := decodeStrictJSON([]byte(`{"decision":{"dm":{"disabled":true,"provider":"jev","endpoint":"https://api.typesafe.ai","model":"jev-latest","api_key_ref":"decision/default"}},"answer_policy":{"evidence":{"disabled":true,"tool":"search_docs","query_argument":"query"}}}`), &cfg); err != nil {
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
	if roundtrip.Decision.DMEnabled() || roundtrip.AnswerPolicy.EvidenceEnabled() {
		t.Fatal("disabled flags lost")
	}
	copy := roundtrip.Clone()
	copy.Decision.DM.Disabled = false
	copy.AnswerPolicy.Evidence.Disabled = false
	if !copy.Decision.DMEnabled() || !copy.AnswerPolicy.EvidenceEnabled() || roundtrip.Decision.DMEnabled() || roundtrip.AnswerPolicy.EvidenceEnabled() {
		t.Fatal("cloned flags alias source")
	}
	copy.Decision.DM.Provider = "typo"
	copy.Decision.DM.Disabled = true
	if err = copy.Decision.Validate(); err == nil {
		t.Fatal("disabled DM bypassed field validation")
	}
}
