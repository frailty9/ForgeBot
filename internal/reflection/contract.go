// Package reflection defines the host-facing control contract for the native
// knowledge reflection module.
package reflection

import (
	"context"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/reflection/buffer"
	"github.com/soleapi/forgebot/internal/reflection/pipeline"
	"github.com/soleapi/forgebot/internal/reflection/scanner"
)

var ControlKey = capability.NewKey[Control]("reflection.control.v1")

type Status struct {
	Enabled  bool           `json:"enabled"`
	Active   bool           `json:"active"`
	Buffer   buffer.Stats   `json:"buffer"`
	Scanner  scanner.Stats  `json:"scanner"`
	Pipeline pipeline.Stats `json:"pipeline"`
}

type ScanResult struct {
	Processed int    `json:"processed"`
	Retried   int    `json:"retried"`
	Skipped   bool   `json:"skipped"`
	Status    Status `json:"status"`
}

type Control interface {
	Scan(context.Context) (ScanResult, error)
	Status() Status
}
