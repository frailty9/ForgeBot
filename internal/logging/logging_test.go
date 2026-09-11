package logging

import (
	"context"
	"log/slog"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
)

func TestDerivedLoggerFollowsLevelUpdate(t *testing.T) {
	logger, controller, err := New(config.LogConfig{Level: "info", Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	derived := logger.With("component", "test")
	if derived.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("debug unexpectedly enabled")
	}
	controller.Apply(config.LogConfig{Level: "debug", Format: "json"})
	if !derived.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("derived logger did not follow update")
	}
}
