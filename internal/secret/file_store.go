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

const fileFormatVersion = 1

type FileStore struct {
	mu      sync.RWMutex
	path    string
	crypter *Crypter
	now     func() time.Time
	state   fileState
}

type fileState struct {
	FormatVersion int                    `json:"format_version"`
	Secrets       map[string]secretEntry `json:"secrets"`
}

type secretEntry struct {
	Version    uint64          `json:"version"`
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
	if err := store.load(); err != nil {
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
	if entry.Candidate != nil && entry.Candidate.Version >= version {
		version = entry.Candidate.Version + 1
	}
	updatedAt := s.now().UTC()
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
	if entry.Ciphertext == "" {
		delete(s.state.Secrets, candidate.Ref)
	} else {
		s.state.Secrets[candidate.Ref] = entry
	}
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
	delete(s.state.Secrets, ref)
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

func (s *FileStore) load() error {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
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
	if state.FormatVersion != fileFormatVersion {
		return fmt.Errorf("unsupported secret store format version %d", state.FormatVersion)
	}
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
	}
	s.state = state
	return nil
}

func (s *FileStore) persistLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
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
		_ = os.Rename(backup, target)
		return err
	}
	_ = os.Remove(backup)
	return nil
}
