package config

import (
	"encoding/json"
	"testing"
)

func TestImageUnderstandingRoundTripAndDefault(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := DefaultRuntime().LLM
		cfg.ImageUnderstanding = enabled
		cfg.Authentication = "none"
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var loaded LLMConfig
		if err = json.Unmarshal(data, &loaded); err != nil {
			t.Fatal(err)
		}
		if loaded.ImageUnderstanding != enabled || loaded.Authentication != "none" {
			t.Fatalf("got %+v", loaded)
		}
	}
	var missing LLMConfig
	if err := json.Unmarshal([]byte(`{"model":"text-only"}`), &missing); err != nil {
		t.Fatal(err)
	}
	if missing.ImageUnderstanding {
		t.Fatal("missing image flag enabled vision")
	}
}
