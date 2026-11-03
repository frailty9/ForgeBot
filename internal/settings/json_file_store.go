package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/operation"
)

const defaultHistoryLimit = 10

type JSONFileStore struct {
	mu           sync.RWMutex
	path         string
	static       config.StaticConfig
	historyLimit int
	now          func() time.Time
	document     config.RuntimeDocument
	recovered    bool
}

func NewJSONFileStore(path string, static config.StaticConfig) (*JSONFileStore, error) {
	return newJSONFileStore(path, static, defaultHistoryLimit, time.Now)
}

func newJSONFileStore(path string, static config.StaticConfig, historyLimit int, now func() time.Time) (*JSONFileStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("runtime config path is required")
	}
	if historyLimit < 1 {
		return nil, errors.New("history limit must be positive")
	}
	if err := static.Validate(); err != nil {
		return nil, err
	}
	document, recovered, err := config.ReadRuntimeDocumentWithRecovery(path, now)
	if err != nil {
		return nil, err
	}
	return &JSONFileStore{path: path, static: static, historyLimit: historyLimit, now: now, document: document, recovered: recovered}, nil
}

func (s *JSONFileStore) Current(_ context.Context) (config.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked(), nil
}

func (s *JSONFileStore) RuntimeAt(_ context.Context, version uint64) (config.RuntimeConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if version == s.document.Version {
		return s.document.Runtime.Clone(), nil
	}
	for _, revision := range s.document.History {
		if revision.Version == version {
			return revision.Runtime.Clone(), nil
		}
	}
	return config.RuntimeConfig{}, ErrVersionNotFound
}

func (s *JSONFileStore) Update(_ context.Context, request UpdateRequest) (config.Snapshot, error) {
	request.Runtime = request.Runtime.Clone()
	if err := request.Runtime.Validate(); err != nil {
		return config.Snapshot{}, operation.Wrap("config.update", operation.Invalid, operation.NotStarted, err)
	}
	if strings.TrimSpace(request.Source) == "" {
		return config.Snapshot{}, operation.New(operation.Invalid, "update source is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateLocked(request)
}

func (s *JSONFileStore) Rollback(_ context.Context, targetVersion, expectedVersion uint64, source string) (config.Snapshot, error) {
	if strings.TrimSpace(source) == "" {
		return config.Snapshot{}, operation.New(operation.Invalid, "rollback source is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedVersion != s.document.Version {
		return config.Snapshot{}, ErrVersionConflict
	}
	var target *config.RuntimeConfig
	if targetVersion == s.document.Version {
		copy := s.document.Runtime
		target = &copy
	} else {
		for _, revision := range s.document.History {
			if revision.Version == targetVersion {
				copy := revision.Runtime
				target = &copy
				break
			}
		}
	}
	if target == nil {
		return config.Snapshot{}, ErrVersionNotFound
	}
	return s.updateLocked(UpdateRequest{ExpectedVersion: expectedVersion, Runtime: *target, Source: source})
}

func (s *JSONFileStore) History(_ context.Context, limit int) ([]RevisionMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := len(s.document.History) + 1
	if limit <= 0 || limit > total {
		limit = total
	}
	result := make([]RevisionMeta, 0, limit)
	result = append(result, RevisionMeta{Version: s.document.Version, UpdatedAt: s.document.UpdatedAt, Source: s.document.Source})
	for i := len(s.document.History) - 1; i >= 0 && len(result) < limit; i-- {
		revision := s.document.History[i]
		result = append(result, RevisionMeta{Version: revision.Version, UpdatedAt: revision.UpdatedAt, Source: revision.Source})
	}
	return result, nil
}

func (s *JSONFileStore) updateLocked(request UpdateRequest) (config.Snapshot, error) {
	if !request.Force && request.ExpectedVersion != s.document.Version {
		return config.Snapshot{}, ErrVersionConflict
	}
	previous := cloneDocument(s.document)
	history := append(s.document.History, config.RuntimeRevision{
		Version: s.document.Version, UpdatedAt: s.document.UpdatedAt,
		Source: s.document.Source, Runtime: s.document.Runtime,
	})
	if overflow := len(history) - s.historyLimit; overflow > 0 {
		history = history[overflow:]
	}
	s.document = config.RuntimeDocument{
		SchemaVersion: config.RuntimeSchemaVersion, ID: config.RuntimeDocumentID,
		Version: s.document.Version + 1, UpdatedAt: s.now().UTC(), Source: request.Source,
		Runtime: request.Runtime.Clone(), History: history,
	}
	if err := s.persistLocked(); err != nil {
		s.document = previous
		return config.Snapshot{}, err
	}
	return s.snapshotLocked(), nil
}

func (s *JSONFileStore) snapshotLocked() config.Snapshot {
	return config.Snapshot{Version: s.document.Version, UpdatedAt: s.document.UpdatedAt, Static: s.static, Runtime: s.document.Runtime.Clone()}
}

func cloneDocument(document config.RuntimeDocument) config.RuntimeDocument {
	copy := document
	copy.History = append([]config.RuntimeRevision(nil), document.History...)
	copy.Runtime = document.Runtime.Clone()
	for index := range copy.History {
		copy.History[index].Runtime = copy.History[index].Runtime.Clone()
	}
	return copy
}

func (s *JSONFileStore) persistLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".forgebot-runtime-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keep := false
	defer func() {
		_ = temp.Close()
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0600); err != nil {
		return err
	}
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s.document); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	backup := s.path + ".bak"
	if s.recovered {
		// 恢复来源仍是有效 .bak；不能先用损坏主文件覆盖它，留下双重故障窗口。
		// 原主文件另存，成功提交前后都保留可读取的恢复版本。
		backup = s.path + ".recovery-invalid"
	}
	if err := replaceFileWithBackup(tempPath, s.path, backup); err != nil {
		return err
	}
	s.recovered = false
	keep = true
	return nil
}

func replaceFileWithBackup(source, target, backup string) error {
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("prepare atomic replace: %w", err)
	}
	if err := os.Rename(source, target); err != nil {
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return operation.Wrap("config.replace", operation.Internal, operation.EffectUnknown, errors.Join(err, fmt.Errorf("restore runtime configuration: %w", restoreErr)))
		}
		return fmt.Errorf("replace runtime config: %w", err)
	}
	return nil
}
