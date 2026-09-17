// Package modules builds whole feature modules into immutable hook chains.
package modules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
)

type Descriptor struct {
	ID             string
	DefaultEnabled bool
	Hooks          []hooks.Stage
}
type Factory interface {
	Descriptor() Descriptor
	ValidateOptions(json.RawMessage) error
	PublicOptions(json.RawMessage) (any, error)
	Build(context.Context, json.RawMessage) (Instance, error)
}
type Instance interface {
	Registrations() hooks.Registrations
	Close(context.Context) error
}
type entry struct {
	descriptor Descriptor
	factory    Factory
	source     hooks.Source
	order      int
	failure    hooks.FailurePolicy
}
type Registry struct{ entries map[string]entry }

func NewRegistry() *Registry { return &Registry{entries: make(map[string]entry)} }

// Register is a composition-time host operation, not a plugin registration API.
func (r *Registry) Register(factory Factory, source hooks.Source, order int, failure hooks.FailurePolicy) error {
	if factory == nil {
		return errors.New("nil module factory")
	}
	descriptor := factory.Descriptor()
	if !config.ValidModuleID(descriptor.ID) {
		return errors.New("invalid module id")
	}
	if _, exists := r.entries[descriptor.ID]; exists {
		return errors.New("duplicate module id")
	}
	if source != hooks.Native && source != hooks.External {
		return errors.New("invalid module source")
	}
	if failure == "" {
		failure = hooks.Continue
	}
	if failure != hooks.Continue && failure != hooks.Abort {
		return errors.New("invalid module failure policy")
	}
	seen := map[hooks.Stage]bool{}
	for _, stage := range descriptor.Hooks {
		if stage != hooks.AgentInput && stage != hooks.BeforeTool && stage != hooks.BeforeReply {
			return fmt.Errorf("unsupported module registration %q", stage)
		}
		if seen[stage] {
			return errors.New("duplicate declared hook")
		}
		seen[stage] = true
	}
	descriptor.Hooks = slices.Clone(descriptor.Hooks)
	r.entries[descriptor.ID] = entry{descriptor, factory, source, order, failure}
	return nil
}

// Clone freezes a private directory for a runtime. Factories must be immutable.
func (r *Registry) Clone() *Registry {
	copy := NewRegistry()
	if r != nil {
		for id, value := range r.entries {
			value.descriptor.Hooks = slices.Clone(value.descriptor.Hooks)
			copy.entries[id] = value
		}
	}
	return copy
}

func (r *Registry) ids() []string {
	var ids []string
	if r != nil {
		for id := range r.entries {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (r *Registry) Validate(entries map[string]config.ModuleConfig) error {
	if err := config.ValidateModules(entries); err != nil {
		return err
	}
	for id := range entries {
		if r == nil {
			return fmt.Errorf("unknown module %q", id)
		}
		if _, exists := r.entries[id]; !exists {
			return fmt.Errorf("unknown module %q", id)
		}
	}
	enabledCount := 0
	for _, id := range r.ids() {
		entry := r.entries[id]
		cfg := entries[id]
		if err := entry.factory.ValidateOptions(slices.Clone(cfg.Options)); err != nil {
			return fmt.Errorf("module %s: %w", id, err)
		}
		if enabled(entry, cfg) {
			enabledCount++
		}
	}
	if enabledCount > config.MaxModules {
		return errors.New("too many enabled modules")
	}
	return nil
}

func enabled(entry entry, cfg config.ModuleConfig) bool {
	if cfg.Enabled != nil {
		return *cfg.Enabled
	}
	return entry.descriptor.DefaultEnabled
}

func (r *Registry) Public(entries map[string]config.ModuleConfig) (map[string]any, error) {
	result := make(map[string]any)
	for _, id := range r.ids() {
		entry := r.entries[id]
		cfg := entries[id]
		options, err := entry.factory.PublicOptions(slices.Clone(cfg.Options))
		if err != nil {
			return nil, err
		}
		result[id] = map[string]any{"enabled": enabled(entry, cfg), "options": options}
	}
	return result, nil
}

type Status struct {
	ID      string              `json:"id"`
	Source  hooks.Source        `json:"source"`
	Enabled bool                `json:"enabled"`
	State   string              `json:"state"`
	Hooks   []hooks.Description `json:"hooks"`
	Error   string              `json:"error,omitempty"`
}
type Set struct {
	chain       *hooks.Chain
	instances   []Instance
	instanceIDs []string
	status      []Status
	mu          sync.RWMutex
	closeOnce   sync.Once
	closeErr    error
}

func (r *Registry) Build(ctx context.Context, entries map[string]config.ModuleConfig, logger *slog.Logger) (result *Set, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = r.Validate(entries); err != nil {
		return nil, err
	}
	set := &Set{status: []Status{}}
	defer func() {
		if err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err = errors.Join(err, set.Close(closeCtx))
		}
	}()
	var mounts []hooks.Mount
	for _, id := range r.ids() {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		entry := r.entries[id]
		cfg := entries[id]
		status := Status{ID: id, Source: entry.source, Enabled: enabled(entry, cfg), State: "disabled", Hooks: []hooks.Description{}}
		if status.Enabled {
			instance, buildErr := entry.factory.Build(ctx, slices.Clone(cfg.Options))
			if instance != nil {
				set.instances = append(set.instances, instance)
				set.instanceIDs = append(set.instanceIDs, id)
			}
			if buildErr != nil {
				return nil, fmt.Errorf("build module %s: %w", id, buildErr)
			}
			if instance == nil {
				return nil, fmt.Errorf("module %s returned nil instance", id)
			}
			registrations := instance.Registrations()
			status.Hooks = hooks.Describe(registrations)
			for _, hook := range status.Hooks {
				if !slices.Contains(entry.descriptor.Hooks, hook.Hook) {
					return nil, fmt.Errorf("module %s registered undeclared hook %s", id, hook.Hook)
				}
			}
			mounts = append(mounts, hooks.Mount{ModuleID: id, Source: entry.source, Order: entry.order, Failure: entry.failure, Registrations: registrations})
			status.State = "prepared"
		}
		set.status = append(set.status, status)
	}
	set.chain, err = hooks.Compile(mounts, logger)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return set, nil
}

func (s *Set) Chain() *hooks.Chain { return s.chain }
func (s *Set) Mark(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.status {
		if s.status[index].Enabled {
			s.status[index].State = state
		}
	}
}
func (s *Set) Status() []Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := slices.Clone(s.status)
	for index := range result {
		result[index].Hooks = slices.Clone(result[index].Hooks)
	}
	return result
}
func (s *Set) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		for index := len(s.instances) - 1; index >= 0; index-- {
			if err := s.instances[index].Close(ctx); err != nil {
				s.closeErr = errors.Join(s.closeErr, fmt.Errorf("close module %s: %w", s.instanceIDs[index], err))
				s.mu.Lock()
				for position := range s.status {
					if s.status[position].ID == s.instanceIDs[index] {
						s.status[position].Error = "close_failed"
					}
				}
				s.mu.Unlock()
			}
		}
		s.Mark("closed")
	})
	return s.closeErr
}
