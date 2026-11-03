// Package structuredsearch exposes local document retrieval as a native Tool,
// and host capability while owning the index lifecycle.
package structuredsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/knowledge"
	"github.com/soleapi/forgebot/internal/knowledge/markdown"
	"github.com/soleapi/forgebot/internal/modules"
	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/search/structured"
	"github.com/soleapi/forgebot/internal/tooling/searchdocs"
)

const ID = "native.structured_search"

type Options struct {
	DocumentRoot      string
	Extensions        []string
	DefaultResults    int
	MaxResults        int
	MinMatchRatio     float64
	QueryTimeout      time.Duration
	BuildTimeout      time.Duration
	MaxQueriesPerTurn int
	MaxContextChars   int
	MaxSectionChars   int
	MaxFileBytes      int64
	MaxTotalBytes     int64
	MaxFiles          int
}

type Factory struct{}

func (Factory) Descriptor() modules.Descriptor {
	return modules.Descriptor{
		ID: ID, DefaultEnabled: false,
		Provides:      []capability.ID{searchcontract.ControlKey.ID(), knowledge.PublisherFactoryKey.ID()},
		Registrations: []modules.RegistrationKind{modules.RegistrationTools, modules.RegistrationStatus},
	}
}

func defaults() Options {
	return Options{Extensions: []string{".md", ".mdx", ".txt"}, DefaultResults: 5, MaxResults: 8, MinMatchRatio: .35, QueryTimeout: 750 * time.Millisecond, BuildTimeout: 20 * time.Second, MaxQueriesPerTurn: 1, MaxContextChars: 12000, MaxSectionChars: 8000, MaxFileBytes: 4 << 20, MaxTotalBytes: 256 << 20, MaxFiles: 10000}
}

type rawOptions struct {
	DocumentRoot      *string   `json:"document_root"`
	Extensions        *[]string `json:"extensions"`
	DefaultResults    *int      `json:"default_results"`
	MaxResults        *int      `json:"max_results"`
	MinMatchRatio     *float64  `json:"min_match_ratio"`
	QueryTimeout      *string   `json:"query_timeout"`
	BuildTimeout      *string   `json:"build_timeout"`
	MaxQueriesPerTurn *int      `json:"max_queries_per_turn"`
	MaxContextChars   *int      `json:"max_context_chars"`
	MaxSectionChars   *int      `json:"max_section_chars"`
	MaxFileBytes      *int64    `json:"max_file_bytes"`
	MaxTotalBytes     *int64    `json:"max_total_bytes"`
	MaxFiles          *int      `json:"max_files"`
}

func parse(raw json.RawMessage) (Options, error) {
	result := defaults()
	if len(raw) == 0 {
		return result, nil
	}
	var value rawOptions
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Options{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Options{}, errors.New("invalid options suffix")
	}
	if value.DocumentRoot != nil {
		result.DocumentRoot = strings.TrimSpace(*value.DocumentRoot)
	}
	if value.Extensions != nil {
		result.Extensions = append([]string(nil), (*value.Extensions)...)
	}
	if value.DefaultResults != nil {
		result.DefaultResults = *value.DefaultResults
	}
	if value.MaxResults != nil {
		result.MaxResults = *value.MaxResults
	}
	if value.MinMatchRatio != nil {
		result.MinMatchRatio = *value.MinMatchRatio
	}
	if value.MaxQueriesPerTurn != nil {
		result.MaxQueriesPerTurn = *value.MaxQueriesPerTurn
	}
	if value.MaxContextChars != nil {
		result.MaxContextChars = *value.MaxContextChars
	}
	if value.MaxSectionChars != nil {
		result.MaxSectionChars = *value.MaxSectionChars
	}
	if value.MaxFileBytes != nil {
		result.MaxFileBytes = *value.MaxFileBytes
	}
	if value.MaxTotalBytes != nil {
		result.MaxTotalBytes = *value.MaxTotalBytes
	}
	if value.MaxFiles != nil {
		result.MaxFiles = *value.MaxFiles
	}
	var err error
	if value.QueryTimeout != nil {
		result.QueryTimeout, err = time.ParseDuration(*value.QueryTimeout)
		if err != nil {
			return Options{}, errors.New("query_timeout must be a duration")
		}
	}
	if value.BuildTimeout != nil {
		result.BuildTimeout, err = time.ParseDuration(*value.BuildTimeout)
		if err != nil {
			return Options{}, errors.New("build_timeout must be a duration")
		}
	}
	if err := validate(result, false); err != nil {
		return Options{}, err
	}
	return result, nil
}

func validate(value Options, requireRoot bool) error {
	if requireRoot && (value.DocumentRoot == "" || !filepath.IsAbs(value.DocumentRoot)) {
		return errors.New("document_root must be absolute")
	}
	if value.DocumentRoot != "" && !filepath.IsAbs(value.DocumentRoot) {
		return errors.New("document_root must be absolute when configured")
	}
	if value.DefaultResults <= 0 || value.MaxResults <= 0 || value.DefaultResults > value.MaxResults {
		return errors.New("result limits are invalid")
	}
	if value.MinMatchRatio <= 0 || value.MinMatchRatio > 1 {
		return errors.New("min_match_ratio is invalid")
	}
	if value.QueryTimeout <= 0 || value.BuildTimeout <= 0 || value.MaxQueriesPerTurn <= 0 || value.MaxContextChars <= 0 || value.MaxSectionChars <= 0 || value.MaxFileBytes <= 0 || value.MaxTotalBytes <= 0 || value.MaxFiles <= 0 {
		return errors.New("search limits must be positive")
	}
	if value.MaxFileBytes > value.MaxTotalBytes {
		return errors.New("max_file_bytes must not exceed max_total_bytes")
	}
	seen := map[string]bool{}
	for _, extension := range value.Extensions {
		extension = strings.ToLower(strings.TrimSpace(extension))
		if extension != ".md" && extension != ".mdx" && extension != ".txt" {
			return fmt.Errorf("unsupported extension %q", extension)
		}
		if seen[extension] {
			return fmt.Errorf("duplicate extension %q", extension)
		}
		seen[extension] = true
	}
	return nil
}

func (Factory) ValidateOptions(raw json.RawMessage) error { _, err := parse(raw); return err }
func (Factory) PublicOptions(raw json.RawMessage) (any, error) {
	value, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return map[string]any{"document_root": value.DocumentRoot, "extensions": value.Extensions, "default_results": value.DefaultResults, "max_results": value.MaxResults, "min_match_ratio": value.MinMatchRatio, "query_timeout": value.QueryTimeout.String(), "build_timeout": value.BuildTimeout.String(), "max_queries_per_turn": value.MaxQueriesPerTurn, "max_context_chars": value.MaxContextChars, "max_section_chars": value.MaxSectionChars, "max_file_bytes": value.MaxFileBytes, "max_total_bytes": value.MaxTotalBytes, "max_files": value.MaxFiles}, nil
}
func (Factory) Build(context.Context, json.RawMessage) (modules.Instance, error) {
	return nil, errors.New("structured search requires build context")
}
func (Factory) BuildWithContext(ctx context.Context, _ modules.BuildContext, raw json.RawMessage) (modules.Instance, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	if err := validate(options, true); err != nil {
		return nil, err
	}
	service, err := structured.New(ctx, structured.Options{Root: options.DocumentRoot, Extensions: options.Extensions, DefaultResults: options.DefaultResults, MaxResults: options.MaxResults, MinMatchRatio: options.MinMatchRatio, QueryTimeout: options.QueryTimeout, BuildTimeout: options.BuildTimeout, MaxSectionChars: options.MaxSectionChars, MaxFileBytes: options.MaxFileBytes, MaxTotalBytes: options.MaxTotalBytes, MaxFiles: options.MaxFiles})
	if err != nil {
		return nil, err
	}
	return &instance{options: options, search: service}, nil
}

type instance struct {
	options Options
	search  searchcontract.Control
}

func (i *instance) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (i *instance) Close(context.Context) error        { return nil }
func (i *instance) ExtendedRegistrations() modules.ExtendedRegistrations {
	return modules.ExtendedRegistrations{
		Tools:        []modules.ToolRegistration{{ID: "search_docs", Tool: searchdocs.New(i.search, i.options.DefaultResults, i.options.MaxResults, i.options.MaxContextChars), MaxCalls: i.options.MaxQueriesPerTurn}},
		Capabilities: []modules.CapabilityRegistration{{ID: searchcontract.ControlKey.ID(), Value: i.search}, {ID: knowledge.PublisherFactoryKey.ID(), Value: publisherFactory{root: i.options.DocumentRoot, maxBytes: i.options.MaxFileBytes, markdown: supportsMarkdown(i.options.Extensions)}}},
		Status:       func() any { return i.search.Status() },
	}
}

type publisherFactory struct {
	root     string
	maxBytes int64
	markdown bool
}

func (f publisherFactory) New(extraDirectory string) (knowledge.Publisher, error) {
	if !f.markdown {
		return nil, errors.New("knowledge publication requires .md in extensions")
	}
	return markdown.New(markdown.Config{Root: f.root, ExtraDirectory: extraDirectory, MaxBytes: f.maxBytes})
}

func supportsMarkdown(extensions []string) bool {
	for _, extension := range extensions {
		if strings.EqualFold(strings.TrimSpace(extension), ".md") {
			return true
		}
	}
	return false
}
