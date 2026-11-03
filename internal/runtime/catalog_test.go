package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/modules/replyexperience"
)

func TestCapabilitiesReflectConfigurationWithoutLeakingSecrets(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Runtime.Decision.DM = &config.DecisionModelConfig{Provider: "jev", APIKeyRef: "decision/private-key-reference"}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry, Secrets: configuredSecrets(t, "answer-private-value"), ClientFactory: (&recordingFactory{}).New, Modules: moduleRegistry(t, replyexperience.Factory{})})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	catalog := manager.Capabilities(context.Background())
	if catalog.Version != snapshot.Version {
		t.Fatal(catalog)
	}
	found := false
	for _, entry := range catalog.Entries {
		if entry.ID == "decision" {
			found = true
			if entry.Configured || !entry.Loaded || entry.Health != "not_probed" {
				t.Fatal("missing DM credential marked healthy", entry)
			}
		}
	}
	if !found {
		t.Fatal("decision capability missing")
	}
	data, _ := json.Marshal(catalog)
	for _, secret := range []string{"answer-private-value", "decision/private-key-reference"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("catalog leaks secret")
		}
	}
}
