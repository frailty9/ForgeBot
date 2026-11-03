// Package search defines the retrieval contract and status model. Concrete
// indexes are responsible for parsing and ranking documents.
package search

import (
	"context"
	"time"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/operation"
)

var (
	ErrDisabled         = operation.New(operation.Disabled, "search is disabled")
	ErrInvalidQuery     = operation.New(operation.Invalid, "search query is invalid")
	ErrReloadInProgress = operation.New(operation.Busy, "search reload is already in progress")
	ErrNoIndex          = operation.New(operation.Unavailable, "search index is unavailable")
)

type State string

const (
	StateDisabled          State = "disabled"
	StateBuilding          State = "building"
	StateReady             State = "ready"
	StateReadyWithWarnings State = "ready_with_warnings"
	StateError             State = "error"
)

type Query struct {
	Text  string `json:"text"`
	Limit int    `json:"limit,omitempty"`
}

type Response struct {
	Query   string        `json:"query"`
	Results []Result      `json:"results"`
	Took    time.Duration `json:"took"`
}

type Result struct {
	SectionID  string   `json:"section_id"`
	Path       string   `json:"path"`
	Title      string   `json:"title,omitempty"`
	Headings   []string `json:"headings,omitempty"`
	Snippet    string   `json:"snippet"`
	CodeLang   string   `json:"code_lang,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Score      float64  `json:"score"`
	MatchRatio float64  `json:"match_ratio"`
}

type Warning struct {
	Path string `json:"path,omitempty"`
	Code string `json:"code"`
}

type ReloadResult struct {
	Documents   int           `json:"documents"`
	Sections    int           `json:"sections"`
	Warnings    int           `json:"warnings"`
	Took        time.Duration `json:"took"`
	CompletedAt time.Time     `json:"completed_at"`
}

type Status struct {
	Enabled        bool          `json:"enabled"`
	Backend        string        `json:"backend,omitempty"`
	State          State         `json:"state"`
	Documents      int           `json:"documents"`
	Sections       int           `json:"sections"`
	LastReloadAt   time.Time     `json:"last_reload_at,omitempty"`
	LastReloadTook time.Duration `json:"last_reload_took,omitempty"`
	LastError      string        `json:"last_error,omitempty"`
	WarningCount   int           `json:"warning_count,omitempty"`
}

type Retriever interface {
	Search(context.Context, Query) (Response, error)
	Reload(context.Context) (ReloadResult, error)
	Status() Status
}

var ControlKey = capability.NewKey[Control]("search.control.v1")

type Control interface {
	Retriever
}
