package runtime

import (
	"context"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/echo"
	"github.com/soleapi/forgebot/internal/platform"
)

func TestBuilderBuildsConnectorSetFromModuleProvider(t *testing.T) {
	registry := modules.NewRegistry()
	if err := registry.Register(echo.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	set, err := registry.Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	cfg := config.PlatformConfig{
		Connectors: []config.ConnectorConfig{{ID: "echo-main", Type: "echo", Enabled: true}},
		Groups:     []config.GroupBindingConfig{{ID: "local", ConnectorID: "echo-main", Target: "target"}},
	}
	connectors, err := (Builder{}).buildConnectors(context.Background(), cfg, set)
	if err != nil {
		t.Fatal(err)
	}
	if statuses := connectors.Status(); len(statuses) != 1 || statuses[0].ID != "echo-main" || statuses[0].State != platform.ConnectorStopped {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestBuilderRejectsMissingEnabledConnectorProvider(t *testing.T) {
	cfg := config.PlatformConfig{Connectors: []config.ConnectorConfig{{ID: "missing", Type: "missing", Enabled: true}}}
	if _, err := (Builder{}).buildConnectors(context.Background(), cfg, nil); err == nil {
		t.Fatal("missing provider accepted")
	}
}
