package echo

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/platform"
)

func TestProviderBuildsMemoryAdapter(t *testing.T) {
	provider := provider{}
	adapter, err := provider.Build(context.Background(), platform.ConnectorBuildRequest{ConnectorID: "echo-main"})
	if err != nil {
		t.Fatal(err)
	}
	if adapter == nil || adapter.ID() != "echo-main" {
		t.Fatalf("adapter = %#v", adapter)
	}
}

func TestProviderRejectsNonEmptyOptions(t *testing.T) {
	provider := provider{}
	if err := provider.ValidateOptions(json.RawMessage(`{"unexpected":true}`), nil, nil); err == nil {
		t.Fatal("module options accepted")
	}
	if err := provider.ValidateOptions(nil, json.RawMessage(`{"unexpected":true}`), nil); err == nil {
		t.Fatal("connector options accepted")
	}
}

func TestFactoryRegistersEchoProvider(t *testing.T) {
	registry := modules.NewRegistry()
	if err := registry.Register(Factory{}, "native", 0, "continue"); err != nil {
		t.Fatal(err)
	}
	set, err := registry.Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	if _, ok := set.ConnectorProvider("echo"); !ok {
		t.Fatal("echo provider not registered")
	}
}
