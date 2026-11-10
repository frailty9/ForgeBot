// Package reflection defines the host-facing control contract for the native
// knowledge reflection module.
package reflection

import (
	"context"
	"time"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/reflection/pipeline"
)

var ControlKey = capability.NewKey[Control]("reflection.control.v1")

type Status struct {
	Enabled  bool           `json:"enabled"`
	Active   bool           `json:"active"`
	Pending  PendingStats   `json:"pending"`
	Worker   WorkerStats    `json:"worker"`
	Pipeline pipeline.Stats `json:"pipeline"`
}

type ScanResult struct {
	Processed int    `json:"processed"`
	Retried   int    `json:"retried"`
	Skipped   bool   `json:"skipped"`
	Status    Status `json:"status"`
}

// PendingStats 只计数已形成的答题会话和模块处理标记，不暴露正文。
type PendingStats struct {
	Sessions int    `json:"sessions"`
	Messages int    `json:"messages"`
	Marks    int    `json:"marks"`
	Dropped  uint64 `json:"dropped"`
	Expired  uint64 `json:"expired"`
}

type WorkerStats struct {
	Running      bool      `json:"running"`
	Scans        uint64    `json:"scans"`
	Skipped      uint64    `json:"skipped"`
	Processed    uint64    `json:"processed"`
	Retried      uint64    `json:"retried"`
	Ready        int       `json:"ready"`
	LastStarted  time.Time `json:"last_started,omitempty"`
	LastFinished time.Time `json:"last_finished,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

type Control interface {
	Scan(context.Context) (ScanResult, error)
	Status() Status
}
