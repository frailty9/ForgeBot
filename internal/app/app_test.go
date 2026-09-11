package app

import (
	"io"
	"log/slog"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
)

func TestNewWithoutSecretStore(t *testing.T) {
	snapshot := config.DefaultSnapshot()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root, err := New(snapshot, logger, nil)
	if err != nil {
		t.Fatal(err)
	}
	if root.runtime == nil || root.server == nil {
		t.Fatal("app dependencies were not initialized")
	}
}
