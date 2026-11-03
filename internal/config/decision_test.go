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
