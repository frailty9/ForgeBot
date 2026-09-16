// Package structured implements bounded, in-memory BM25F search over local
// Markdown, MDX, and plain-text documents.
package structured

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	searchcontract "github.com/soleapi/forgebot/internal/search"
)

// Options controls document loading, indexing, ranking, and resource limits.
type Options struct {
	Root            string
	Extensions      []string
	DefaultResults  int
	MaxResults      int
	MinMatchRatio   float64
	QueryTimeout    time.Duration
	BuildTimeout    time.Duration
	MaxSectionChars int
	MaxFileBytes    int64
	MaxTotalBytes   int64
	MaxFiles        int
}

type snapshot struct {
	index       *Index
	warnings    []searchcontract.Warning
	completedAt time.Time
	took        time.Duration
}

// Service owns the active index and performs atomic reloads.
type Service struct {
	opts     Options
	current  atomic.Pointer[snapshot]
	reloadMu sync.Mutex
	statusMu sync.RWMutex
	status   searchcontract.Status
}

// New builds the initial index before returning a ready service.
func New(ctx context.Context, opts Options) (*Service, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	service := &Service{opts: opts, status: searchcontract.Status{Enabled: true, Backend: "structured", State: searchcontract.StateBuilding}}
	if _, err := service.reload(ctx, false); err != nil {
		return nil, err
	}
	return service, nil
}

func (o Options) validate() error {
	if strings.TrimSpace(o.Root) == "" {
		return errors.New("structured search root is required")
	}
	if len(o.Extensions) == 0 {
		return errors.New("structured search extensions are required")
	}
	seenExtensions := make(map[string]struct{}, len(o.Extensions))
	for _, extension := range o.Extensions {
		extension = strings.ToLower(strings.TrimSpace(extension))
		if extension != ".md" && extension != ".mdx" && extension != ".txt" {
			return fmt.Errorf("structured search extension %q is not supported", extension)
		}
		if _, exists := seenExtensions[extension]; exists {
			return fmt.Errorf("structured search extension %q is duplicated", extension)
		}
		seenExtensions[extension] = struct{}{}
	}
	if o.DefaultResults <= 0 || o.MaxResults <= 0 || o.DefaultResults > o.MaxResults {
		return errors.New("structured search result limits are invalid")
	}
	if o.MinMatchRatio <= 0 || o.MinMatchRatio > 1 {
		return errors.New("structured search minimum match ratio is invalid")
	}
	if o.QueryTimeout <= 0 || o.BuildTimeout <= 0 {
		return errors.New("structured search timeouts must be positive")
	}
	if o.MaxSectionChars <= 0 || o.MaxFileBytes <= 0 || o.MaxTotalBytes <= 0 || o.MaxFiles <= 0 {
		return errors.New("structured search resource limits must be positive")
	}
	return nil
}

// Search ranks sections in the current immutable index.
func (s *Service) Search(ctx context.Context, query searchcontract.Query) (searchcontract.Response, error) {
	snap := s.current.Load()
	if snap == nil || snap.index == nil {
		return searchcontract.Response{}, searchcontract.ErrNoIndex
	}
	if query.Limit <= 0 {
		query.Limit = s.opts.DefaultResults
	}
	if query.Limit > s.opts.MaxResults {
		query.Limit = s.opts.MaxResults
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.opts.QueryTimeout)
	defer cancel()
	return snap.index.Search(queryCtx, query, s.opts.MinMatchRatio)
}

// Reload builds a candidate index and swaps it in only after a successful build.
func (s *Service) Reload(ctx context.Context) (searchcontract.ReloadResult, error) {
	return s.reload(ctx, true)
}

func (s *Service) reload(ctx context.Context, try bool) (searchcontract.ReloadResult, error) {
	if try {
		if !s.reloadMu.TryLock() {
			return searchcontract.ReloadResult{}, searchcontract.ErrReloadInProgress
		}
	} else {
		s.reloadMu.Lock()
	}
	defer s.reloadMu.Unlock()
	s.setBuilding()
	buildCtx, cancel := context.WithTimeout(ctx, s.opts.BuildTimeout)
	defer cancel()
	started := time.Now()
	index, warnings, err := build(buildCtx, s.opts)
	if err != nil {
		s.setError(err)
		return searchcontract.ReloadResult{}, err
	}
	completed := time.Now().UTC()
	took := time.Since(started)
	snap := &snapshot{index: index, warnings: warnings, completedAt: completed, took: took}
	s.current.Store(snap)
	s.setReady(snap)
	return searchcontract.ReloadResult{Documents: index.documents, Sections: len(index.sections), Warnings: len(warnings), Took: took, CompletedAt: completed}, nil
}

func (s *Service) Status() searchcontract.Status {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.status
}

func (s *Service) setBuilding() {
	s.statusMu.Lock()
	s.status.State = searchcontract.StateBuilding
	s.status.LastError = ""
	s.statusMu.Unlock()
}
func (s *Service) setError(err error) {
	s.statusMu.Lock()
	s.status.LastError = err.Error()
	if s.current.Load() == nil {
		s.status.State = searchcontract.StateError
	} else {
		snap := s.current.Load()
		if len(snap.warnings) > 0 {
			s.status.State = searchcontract.StateReadyWithWarnings
		} else {
			s.status.State = searchcontract.StateReady
		}
	}
	s.statusMu.Unlock()
}
func (s *Service) setReady(snap *snapshot) {
	s.statusMu.Lock()
	state := searchcontract.StateReady
	if len(snap.warnings) > 0 {
		state = searchcontract.StateReadyWithWarnings
	}
	s.status = searchcontract.Status{Enabled: true, Backend: "structured", State: state, Documents: snap.index.documents, Sections: len(snap.index.sections), LastReloadAt: snap.completedAt, LastReloadTook: snap.took, WarningCount: len(snap.warnings)}
	s.statusMu.Unlock()
}

func build(ctx context.Context, opts Options) (*Index, []searchcontract.Warning, error) {
	root, err := filepath.Abs(strings.TrimSpace(opts.Root))
	if err != nil {
		return nil, nil, fmt.Errorf("resolve document root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve document root links: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, nil, fmt.Errorf("stat document root: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("document root is not a directory")
	}
	extensions := make(map[string]struct{}, len(opts.Extensions))
	for _, ext := range opts.Extensions {
		extensions[strings.ToLower(ext)] = struct{}{}
	}
	ignored := map[string]struct{}{".git": {}, ".next": {}, ".nuxt": {}, ".output": {}, "build": {}, "coverage": {}, "dist": {}, "node_modules": {}}
	var paths []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if path != root && entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path != root {
				if _, ok := ignored[strings.ToLower(entry.Name())]; ok {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if entry.Type().IsRegular() {
			if _, ok := extensions[strings.ToLower(filepath.Ext(entry.Name()))]; ok {
				paths = append(paths, path)
				if len(paths) > opts.MaxFiles {
					return fmt.Errorf("document count exceeds max_files")
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("scan document root: %w", err)
	}
	sort.Strings(paths)
	documents := make([]Document, 0, len(paths))
	warnings := make([]searchcontract.Warning, 0)
	total := int64(0)
	parser := newParser(opts.MaxSectionChars)
	for _, path := range paths {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil {
			warnings = append(warnings, searchcontract.Warning{Path: relative(root, path), Code: "resolve_failed"})
			continue
		}
		if !inside(root, resolved) {
			warnings = append(warnings, searchcontract.Warning{Path: relative(root, path), Code: "path_outside_root"})
			continue
		}
		fileInfo, statErr := os.Lstat(resolved)
		if statErr != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode()&os.ModeSymlink != 0 {
			warnings = append(warnings, searchcontract.Warning{Path: relative(root, path), Code: "not_regular"})
			continue
		}
		if fileInfo.Size() > opts.MaxFileBytes {
			warnings = append(warnings, searchcontract.Warning{Path: relative(root, path), Code: "file_too_large"})
			continue
		}
		if total+fileInfo.Size() > opts.MaxTotalBytes {
			return nil, nil, fmt.Errorf("document bytes exceed max_total_bytes")
		}
		data, readErr := readBounded(resolved, opts.MaxFileBytes)
		if readErr != nil {
			warnings = append(warnings, searchcontract.Warning{Path: relative(root, path), Code: "read_failed"})
			continue
		}
		total += int64(len(data))
		document, parseErr := parser.Parse(relative(root, path), data)
		if parseErr != nil {
			warnings = append(warnings, searchcontract.Warning{Path: relative(root, path), Code: "parse_failed"})
			continue
		}
		documents = append(documents, document)
	}
	return newIndex(documents), warnings, nil
}

func readBounded(path string, max int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("file exceeds limit")
	}
	return data, nil
}
func relative(root, path string) string {
	value, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.ToSlash(value)
}
func inside(root, path string) bool {
	value, err := filepath.Rel(root, path)
	return err == nil && value != ".." && !strings.HasPrefix(value, ".."+string(filepath.Separator)) && !filepath.IsAbs(value)
}
