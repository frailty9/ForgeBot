package runtime

import (
	"context"
	"testing"

	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/operation"
)

func TestRequiredModelRejectsMissingConfiguration(t *testing.T) {
	ctx := context.Background()
	for _, model := range []string{"", "configured-model"} {
		snapshot := testSnapshot()
		snapshot.Runtime.LLM.Model = model
		_, err := (Builder{RequireLLM: true}).buildClient(ctx, snapshot, "", "")
		if err == nil || operation.Describe(err).Code != operation.NotConfigured {
			t.Fatalf("model=%q err=%v", model, err)
		}
	}
	snapshot := testSnapshot()
	snapshot.Runtime.LLM.Authentication = "none"
	called := false
	client, err := (Builder{RequireLLM: true, ClientFactory: func(cfg llm.ClientConfig) (llm.Client, error) {
		called = true
		if cfg.APIKey != "" {
			t.Fatal("anonymous auth leaked key")
		}
		return &responseClient{protocol: cfg.Protocol, text: "local"}, nil
	}}).buildClient(ctx, snapshot, snapshot.Runtime.LLM.APIKeyRef, "must-not-send")
	if err != nil || client == nil || !called {
		t.Fatalf("anonymous client: %v", err)
	}
}

func TestInvalidRequiredModelUpdateKeepsWorkingGeneration(t *testing.T) {
	snapshot := testSnapshot()
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry, RequireLLM: true, Secrets: configuredSecrets(t, "key"), ClientFactory: (&recordingFactory{}).New})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	next := snapshot.Clone()
	next.Version++
	next.Runtime.LLM.Model = ""
	if _, err = manager.Apply(context.Background(), next); err == nil {
		t.Fatal("missing model replaced current generation")
	}
	current, _ := manager.Snapshot()
	if current.Runtime.LLM.Model != snapshot.Runtime.LLM.Model || current.Version != snapshot.Version {
		t.Fatal("failed candidate published")
	}
}
