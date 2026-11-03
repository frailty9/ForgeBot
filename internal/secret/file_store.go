package secret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const fileFormatVersion = 2

type FileStore struct {
	mu      sync.RWMutex
	path    string
	crypter *Crypter
	now     func() time.Time
	state   fileState
	legacy  bool
}

type fileState struct {
	FormatVersion int                    `json:"format_version"`
	Secrets       map[string]secretEntry `json:"secrets"`
}

type secretEntry struct {
	Version    uint64          `json:"version"`
	Sequence   uint64          `json:"sequence,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at"`
	Ciphertext string          `json:"ciphertext"`
	Candidate  *candidateEntry `json:"candidate,omitempty"`
}

type candidateEntry struct {
	Version    uint64    `json:"version"`
	UpdatedAt  time.Time `json:"updated_at"`
	Ciphertext string    `json:"ciphertext"`
}

func NewFileStore(path string, key []byte) (*FileStore, error) {
	return newFileStore(path, key, time.Now)
}

func newFileStore(path string, key []byte, now func() time.Time) (*FileStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("secret store path is required")
	}
	crypter, err := NewCrypter(key)
	if err != nil {
		return nil, err
	}
	store := &FileStore{path: path, crypter: crypter, now: now, state: fileState{FormatVersion: fileFormatVersion, Secrets: map[string]secretEntry{}}}
	if err := store.load(true); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileStore) Status(_ context.Context, ref string) (Status, error) {
	if err := validateRef(ref); err != nil {
		return Status{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.state.Secrets[ref]
	if !ok || entry.Ciphertext == "" {
		return Status{}, nil
	}
	return Status{Configured: true, Version: entry.Version, UpdatedAt: entry.UpdatedAt}, nil
}

func (s *FileStore) Get(_ context.Context, ref string) (string, error) {
	if err := validateRef(ref); err != nil {
		return "", err
	}
	s.mu.RLock()
	entry, ok := s.state.Secrets[ref]
	s.mu.RUnlock()
	if !ok || entry.Ciphertext == "" {
		return "", ErrNotFound
	}
	plain, err := s.crypter.Decrypt(entry.Ciphertext)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *FileStore) Stage(_ context.Context, ref, value string) (Candidate, error) {
	if err := validateRef(ref); err != nil {
		return Candidate{}, err
	}
	if strings.TrimSpace(value) == "" {
		return Candidate{}, errors.New("secret value must not be empty")
	}
	ciphertext, err := s.crypter.Encrypt([]byte(value))
	if err != nil {
		return Candidate{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.state.Secrets[ref]
	version := entry.Version + 1
	if entry.Sequence >= version {
		version = entry.Sequence + 1
	}
	if entry.Candidate != nil && entry.Candidate.Version >= version {
		version = entry.Candidate.Version + 1
	}
	updatedAt := s.now().UTC()
	entry.Sequence = version
	entry.Candidate = &candidateEntry{Version: version, UpdatedAt: updatedAt, Ciphertext: ciphertext}
	previous, existed := s.state.Secrets[ref]
	s.state.Secrets[ref] = entry
	if err := s.persistLocked(); err != nil {
		restoreEntry(s.state.Secrets, ref, previous, existed)
		return Candidate{}, err
	}
	return Candidate{Ref: ref, Version: version, UpdatedAt: updatedAt}, nil
}

func (s *FileStore) Activate(_ context.Context, candidate Candidate) (Status, error) {
	if err := validateRef(candidate.Ref); err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.state.Secrets[candidate.Ref]
	if !ok || entry.Candidate == nil || entry.Candidate.Version != candidate.Version {
		return Status{}, ErrCandidateNotFound
	}
	entry.Version = entry.Candidate.Version
	entry.UpdatedAt = entry.Candidate.UpdatedAt
	entry.Ciphertext = entry.Candidate.Ciphertext
	entry.Candidate = nil
	previous := s.state.Secrets[candidate.Ref]
	s.state.Secrets[candidate.Ref] = entry
	if err := s.persistLocked(); err != nil {
		s.state.Secrets[candidate.Ref] = previous
		return Status{}, err
	}
	return Status{Configured: true, Version: entry.Version, UpdatedAt: entry.UpdatedAt}, nil
}

func (s *FileStore) Discard(_ context.Context, candidate Candidate) error {
	if err := validateRef(candidate.Ref); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.state.Secrets[candidate.Ref]
	if !ok || entry.Candidate == nil || entry.Candidate.Version != candidate.Version {
		return ErrCandidateNotFound
	}
	entry.Candidate = nil
	previous := s.state.Secrets[candidate.Ref]
	// 保留分配序列的墓碑，避免丢弃后重建出同版本候选，导致过期句柄误激活。
	s.state.Secrets[candidate.Ref] = entry
	if err := s.persistLocked(); err != nil {
		s.state.Secrets[candidate.Ref] = previous
		return err
	}
	return nil
}

func (s *FileStore) Delete(_ context.Context, ref string) error {
	if err := validateRef(ref); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.state.Secrets[ref]
	if !existed {
		return nil
	}
	entry := previous
	if entry.Version > entry.Sequence {
		entry.Sequence = entry.Version
	}
	if entry.Candidate != nil && entry.Candidate.Version > entry.Sequence {
		entry.Sequence = entry.Candidate.Version
	}
	entry.Ciphertext = ""
	entry.Candidate = nil
	s.state.Secrets[ref] = entry
	if err := s.persistLocked(); err != nil {
		restoreEntry(s.state.Secrets, ref, previous, existed)
		return err
	}
	return nil
}

func restoreEntry(entries map[string]secretEntry, ref string, previous secretEntry, existed bool) {
	if existed {
		entries[ref] = previous
	} else {
		delete(entries, ref)
	}
}

func validateRef(ref string) error {
	if strings.TrimSpace(ref) == "" {
		return errors.New("secret reference is required")
	}
	if strings.ContainsAny(ref, "\\\r\n\t") || strings.Contains(ref, "..") || strings.HasPrefix(ref, "/") {
		return errors.New("invalid secret reference")
	}
	return nil
}

// InspectFile 供诊断使用，可读取有效恢复文件但不恢复/改写任何部署文件。
func InspectFile(path string, key []byte) (interface {
	Reader
	StatusReader
}, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("secret store path is required")
	}
	crypter, err := NewCrypter(key)
	if err != nil {
		return nil, err
	}
	store := &FileStore{path: path, crypter: crypter, now: time.Now, state: fileState{FormatVersion: fileFormatVersion, Secrets: map[string]secretEntry{}}}
	if err = store.load(false); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileStore) load(restore bool) error {
	file, err := os.Open(s.path)
	recovered := false
	if errors.Is(err, os.ErrNotExist) {
		// Windows 替换的两次 rename 之间崩溃时，主文件可能暂时缺失。
		// 有恢复文件就必须验证并恢复，不能将已有凭据静默视作空仓储。
		file, err = os.Open(s.path + ".replace-backup")
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		recovered = true
	}
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var state fileState
	if err := decoder.Decode(&state); err != nil {
		return fmt.Errorf("decode secret store: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("secret store must contain one JSON value")
	}
	if state.FormatVersion != 1 && state.FormatVersion != fileFormatVersion {
		return fmt.Errorf("unsupported secret store format version %d", state.FormatVersion)
	}
	// 旧格式的 active/candidate 版本足以恢复分配序列；更新时再写新格式。
	s.legacy = state.FormatVersion == 1
	state.FormatVersion = fileFormatVersion
	if state.Secrets == nil {
		state.Secrets = map[string]secretEntry{}
	}
	for ref, entry := range state.Secrets {
		if err := validateRef(ref); err != nil {
			return fmt.Errorf("secret store contains invalid reference: %w", err)
		}
		if entry.Ciphertext != "" {
			if _, err := s.crypter.Decrypt(entry.Ciphertext); err != nil {
				return fmt.Errorf("validate secret %q: %w", ref, err)
			}
		}
		if entry.Candidate != nil {
			if _, err := s.crypter.Decrypt(entry.Candidate.Ciphertext); err != nil {
				return fmt.Errorf("validate candidate %q: %w", ref, err)
			}
		}
		if entry.Version > entry.Sequence {
			entry.Sequence = entry.Version
		}
		if entry.Candidate != nil && entry.Candidate.Version > entry.Sequence {
			entry.Sequence = entry.Candidate.Version
		}
		state.Secrets[ref] = entry
	}
	s.state = state
	if recovered && restore {
		if err := file.Close(); err != nil {
			return err
		}
		if err := os.Rename(s.path+".replace-backup", s.path); err != nil {
			return fmt.Errorf("restore interrupted credential replacement: %w", err)
		}
	}
	return nil
}

func (s *FileStore) persistLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if s.legacy {
		if err := s.backupLegacy(); err != nil {
			return err
		}
	}
	temp, err := os.CreateTemp(dir, ".forgebot-secrets-*")
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
	if err := encoder.Encode(s.state); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceFile(tempPath, s.path); err != nil {
		return err
	}
	keep = true
	s.legacy = false
	return nil
}

// 数据格式升级先保留原始密文文件；不因允许破坏式接口迭代而丢弃部署数据。
func (s *FileStore) backupLegacy() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	path := s.path + ".format-v1.bak"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		previous, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if string(previous) != string(data) {
			return fmt.Errorf("legacy credential backup already exists with different content")
		}
		return nil
	}
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

func replaceFile(source, target string) error {
	if err := os.Rename(source, target); err == nil {
		return nil
	}
	backup := target + ".replace-backup"
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(source, target); err != nil {
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore credentials: %w", restoreErr))
		}
		return err
	}
	_ = os.Remove(backup)
	return nil
}
