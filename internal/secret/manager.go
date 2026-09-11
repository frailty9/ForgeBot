package secret

import (
	"context"
	"errors"
	"sync"
)

type Validator func(context.Context, string, string) error
type Committer func(context.Context, string, string, func() error) error
type Deleter func(context.Context, string, func() error) error

// Manager exposes write-only secret lifecycle operations to management layers.
// Reading plaintext remains an internal runtime concern through Store.Get.
type Manager struct {
	mu       sync.Mutex
	store    Store
	validate Validator
	commit   Committer
	remove   Deleter
}

func NewManagedWriter(store Store, commit Committer, remove Deleter) (*Manager, error) {
	if store == nil {
		return nil, errors.New("secret store is required")
	}
	if commit == nil {
		return nil, errors.New("secret committer is required")
	}
	if remove == nil {
		return nil, errors.New("secret deleter is required")
	}
	return &Manager{store: store, commit: commit, remove: remove}, nil
}

func NewManager(store Store, validate Validator) (*Manager, error) {
	if store == nil {
		return nil, errors.New("secret store is required")
	}
	if validate == nil {
		return nil, errors.New("secret validator is required")
	}
	return &Manager{store: store, validate: validate}, nil
}

func (m *Manager) Status(ctx context.Context, ref string) (Status, error) {
	return m.store.Status(ctx, ref)
}

func (m *Manager) Replace(ctx context.Context, ref, value string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	candidate, err := m.store.Stage(ctx, ref, value)
	if err != nil {
		return Status{}, err
	}
	activate := func() error {
		_, activateErr := m.store.Activate(ctx, candidate)
		return activateErr
	}
	if m.commit != nil {
		err = m.commit(ctx, ref, value, activate)
	} else if err = m.validate(ctx, ref, value); err == nil {
		err = activate()
	}
	if err != nil {
		if discardErr := m.store.Discard(ctx, candidate); discardErr != nil {
			if !errors.Is(discardErr, ErrCandidateNotFound) {
				return Status{}, errors.Join(err, discardErr)
			}
		}
		return Status{}, err
	}
	return m.store.Status(ctx, ref)
}

func (m *Manager) Delete(ctx context.Context, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.remove != nil {
		return m.remove(ctx, ref, func() error { return m.store.Delete(ctx, ref) })
	}
	return m.store.Delete(ctx, ref)
}
