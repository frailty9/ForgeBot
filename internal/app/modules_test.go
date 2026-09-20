package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules/replyexperience"
)

func TestAppRegistersDefaultNativeModule(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		snapshot := config.DefaultSnapshot()
		if disabled {
			enabled := false
			snapshot.Runtime.Modules = map[string]config.ModuleConfig{replyexperience.ID: {Enabled: &enabled}}
		}
		root, err := New(snapshot, nil, nil)
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
		if len(status.Modules) != 3 || status.Modules[0].Enabled == disabled || status.Modules[1].Enabled || status.Modules[2].Enabled {
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
	if _, err := New(snapshot, nil, nil); err == nil {
		t.Fatal("invalid module accepted")
	}
}
