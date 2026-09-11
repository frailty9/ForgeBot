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

type UpdateRequest struct {
	ExpectedVersion uint64
	Runtime         config.RuntimeConfig
	Source          string
	Force           bool
}

type RevisionMeta struct {
	Version   uint64    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	Source    string    `json:"source,omitempty"`
}

type Store interface {
	Current(context.Context) (config.Snapshot, error)
	RuntimeAt(context.Context, uint64) (config.RuntimeConfig, error)
	Update(context.Context, UpdateRequest) (config.Snapshot, error)
	Rollback(context.Context, uint64, uint64, string) (config.Snapshot, error)
	History(context.Context, int) ([]RevisionMeta, error)
}
