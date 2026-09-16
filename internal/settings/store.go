// Package settings persists versioned runtime configuration and exposes
// optimistic-concurrency update operations.
package settings

import (
	"context"
	"errors"
	"time"

	"github.com/soleapi/forgebot/internal/config"
)

var (
	ErrVersionConflict = errors.New("runtime configuration version conflict")
	ErrVersionNotFound = errors.New("runtime configuration version not found")
)

// UpdateRequest describes a complete runtime replacement with concurrency guard.
type UpdateRequest struct {
	ExpectedVersion uint64
	Runtime         config.RuntimeConfig
	Source          string
	Force           bool
}

// RevisionMeta is the redacted metadata for one persisted runtime revision.
type RevisionMeta struct {
	Version   uint64    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	Source    string    `json:"source,omitempty"`
}

// Store persists versioned runtime documents and supports rollback.
type Store interface {
	Current(context.Context) (config.Snapshot, error)
	RuntimeAt(context.Context, uint64) (config.RuntimeConfig, error)
	Update(context.Context, UpdateRequest) (config.Snapshot, error)
	Rollback(context.Context, uint64, uint64, string) (config.Snapshot, error)
	History(context.Context, int) ([]RevisionMeta, error)
}
