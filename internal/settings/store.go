// Package settings persists versioned runtime configuration and exposes
// optimistic-concurrency update operations.
package settings

import (
	"context"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/operation"
)

var (
	ErrVersionConflict = operation.New(operation.Conflict, "runtime configuration version conflict")
	ErrVersionNotFound = operation.New(operation.NotFound, "runtime configuration version not found")
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

// Reader 只提供当前配置快照；消费者不能从返回对象修改仓储内部状态。
type Reader interface {
	Current(context.Context) (config.Snapshot, error)
}

// HistoryReader 提供领域版本查询，不暴露文件路径或底层数据库句柄。
type HistoryReader interface {
	RuntimeAt(context.Context, uint64) (config.RuntimeConfig, error)
	History(context.Context, int) ([]RevisionMeta, error)
}

// Writer 使用 ExpectedVersion 保证条件更新；回滚也是一个新版本。
type Writer interface {
	Update(context.Context, UpdateRequest) (config.Snapshot, error)
	Rollback(context.Context, uint64, uint64, string) (config.Snapshot, error)
}

// Store 是后端提供的配置领域仓储，不要求普通消费者依赖全部方法。
type Store interface {
	Reader
	HistoryReader
	Writer
}
