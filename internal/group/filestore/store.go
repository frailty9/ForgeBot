// Package filestore 是群运营仓储的文件实现，由唯一 persistence.Backend 创建。
package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/operation"
)

const maxRecords = 10000

type document struct {
	SchemaVersion int                     `json:"schema_version"`
	Records       map[string]group.Record `json:"records"`
}
type Store struct {
	mu   sync.Mutex
	path string
	data document
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, data: document{SchemaVersion: 1, Records: map[string]group.Record{}}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data, err = os.ReadFile(path + ".replace-backup")
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 32<<20 {
		return nil, operation.New(operation.Invalid, "group operation store exceeds size limit")
	}
	if err = json.Unmarshal(data, &s.data); err != nil {
		return nil, err
	}
	if s.data.SchemaVersion != 1 || len(s.data.Records) > maxRecords {
		return nil, operation.New(operation.Unsupported, "unsupported group operation store format")
	}
	if s.data.Records == nil {
		s.data.Records = map[string]group.Record{}
	}
	for id, record := range s.data.Records {
		if id != record.ID || record.SchemaVersion != 1 || record.Revision == 0 {
			return nil, operation.New(operation.Invalid, "invalid group operation identity")
		}
		// 上次进程在执行中退出，远端是否完成未知；绝不能重启后自动重放。
		if record.Status == group.Executing {
			record.Status = group.Uncertain
			record.Failure = &operation.Failure{Code: operation.Unknown, Effect: operation.EffectUnknown}
			s.data.Records[id] = record
		}
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err = os.Rename(path+".replace-backup", path); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func clone(r group.Record) group.Record {
	if r.Failure != nil {
		copy := *r.Failure
		r.Failure = &copy
	}
	return r
}
func (s *Store) Get(ctx context.Context, id string) (group.Record, error) {
	if err := ctx.Err(); err != nil {
		return group.Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.data.Records[id]
	if !ok {
		return r, operation.New(operation.NotFound, "group operation not found")
	}
	return clone(r), nil
}
func (s *Store) List(ctx context.Context, limit int) ([]group.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	result := make([]group.Record, 0, len(s.data.Records))
	for _, r := range s.data.Records {
		result = append(result, clone(r))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
func (s *Store) Create(ctx context.Context, r group.Record) (group.Record, bool, error) {
	if err := ctx.Err(); err != nil {
		return r, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, exists := s.data.Records[r.ID]; exists {
		return clone(old), false, nil
	}
	if len(s.data.Records) >= maxRecords {
		return r, false, operation.New(operation.Busy, "group operation store capacity exhausted")
	}
	if r.ID == "" || len(r.ID) > 128 || r.ConversationID == "" || len(r.Reference) > 4096 || len(r.Comment) > 8192 || r.CreatedAt.IsZero() {
		return r, false, operation.New(operation.Invalid, "invalid group operation record")
	}
	r.SchemaVersion = 1
	r.Revision = 1
	s.data.Records[r.ID] = clone(r)
	if err := s.persist(); err != nil {
		delete(s.data.Records, r.ID)
		return r, false, err
	}
	return clone(r), true, nil
}
func (s *Store) Update(ctx context.Context, expected uint64, r group.Record) (group.Record, error) {
	if err := ctx.Err(); err != nil {
		return r, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.data.Records[r.ID]
	if !exists {
		return r, operation.New(operation.NotFound, "group operation not found")
	}
	if old.Revision != expected {
		return r, operation.New(operation.Conflict, "group operation revision conflict")
	}
	if r.Kind != old.Kind || r.ConversationID != old.ConversationID || r.ConnectorID != old.ConnectorID || r.UserID != old.UserID || !r.CreatedAt.Equal(old.CreatedAt) || !r.ExpiresAt.Equal(old.ExpiresAt) || r.Reference != old.Reference || r.Fingerprint != old.Fingerprint {
		return r, operation.New(operation.Invalid, "group operation identity cannot change")
	}
	r.Revision = old.Revision + 1
	r.SchemaVersion = 1
	s.data.Records[r.ID] = clone(r)
	if err := s.persist(); err != nil {
		s.data.Records[r.ID] = old
		return r, err
	}
	return clone(r), nil
}
func (s *Store) persist() error {
	data, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	if len(data) > 32<<20 {
		return operation.New(operation.Busy, "group operation store exceeds size limit")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".forgebot-groups-*")
	if err != nil {
		return err
	}
	path := f.Name()
	defer func() { _ = f.Close(); _ = os.Remove(path) }()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(path, s.path); err == nil {
		return nil
	}
	backup := s.path + ".replace-backup"
	if err = os.Rename(s.path, backup); err != nil {
		return err
	}
	if err = os.Rename(path, s.path); err != nil {
		if restoreErr := os.Rename(backup, s.path); restoreErr != nil {
			return operation.Wrap("group.store.replace", operation.Internal, operation.EffectUnknown, errors.Join(err, fmt.Errorf("restore operations: %w", restoreErr)))
		}
		return err
	}
	_ = os.Remove(backup)
	return nil
}
