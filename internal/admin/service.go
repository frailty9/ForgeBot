// Package admin adapts runtime, settings, secrets, search, and reflection
// operations to the HTTP management surface.
package admin

import (
	"context"
	"errors"
	"strings"

	"github.com/soleapi/forgebot/internal/config"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/secret"
	"github.com/soleapi/forgebot/internal/settings"
)

var ErrUnavailable = errors.New("management capability is unavailable")

// Service is the management facade used by the HTTP server.
type Service struct {
	runtime  *runtimecfg.Manager
	settings settings.Store
	secrets  *secret.Manager
}

// UpdateRequest is a complete runtime replacement guarded by expected_version.
type UpdateRequest struct {
	ExpectedVersion uint64        `json:"expected_version"`
	Runtime         RuntimeConfig `json:"runtime"`
	Source          string        `json:"source"`
}

// RollbackRequest identifies a persisted runtime revision to restore.
type RollbackRequest struct {
	TargetVersion   uint64 `json:"target_version"`
	ExpectedVersion uint64 `json:"expected_version"`
	Source          string `json:"source"`
}

// New validates management dependencies and creates the facade.
func New(runtime *runtimecfg.Manager, store settings.Store, secrets *secret.Manager) (*Service, error) {
	if runtime == nil {
		return nil, errors.New("runtime manager is required")
	}
	return &Service{runtime: runtime, settings: store, secrets: secrets}, nil
}

func (s *Service) Config(ctx context.Context) (ConfigResponse, error) {
	snapshot, status := s.runtime.Snapshot()
	var secretStatus secret.Status
	if s.secrets != nil {
		var err error
		secretStatus, err = s.secrets.Status(ctx, snapshot.Runtime.LLM.APIKeyRef)
		if err != nil {
			return ConfigResponse{}, err
		}
	}
	return configResponse(snapshot, status, secretStatus), nil
}

func (s *Service) resolveRuntime(runtime RuntimeConfig) (config.RuntimeConfig, error) {
	snapshot, _ := s.runtime.Snapshot()
	resolved, err := runtime.resolve(snapshot.Runtime)
	if err != nil {
		return config.RuntimeConfig{}, err
	}
	return resolved, nil
}

func (s *Service) History(ctx context.Context, limit int) ([]settings.RevisionMeta, error) {
	if s.settings == nil {
		return nil, ErrUnavailable
	}
	return s.settings.History(ctx, limit)
}

func (s *Service) Validate(runtime RuntimeConfig) error {
	resolved, err := s.resolveRuntime(runtime)
	if err != nil {
		return err
	}
	if err := resolved.Validate(); err != nil {
		return &FieldError{Field: "runtime", Err: err}
	}
	return nil
}

func (s *Service) Update(ctx context.Context, request UpdateRequest) (runtimecfg.ApplyResult, error) {
	if s.settings == nil {
		return runtimecfg.ApplyResult{}, ErrUnavailable
	}
	resolved, err := s.resolveRuntime(request.Runtime)
	if err != nil {
		return runtimecfg.ApplyResult{}, err
	}
	request.Source = strings.TrimSpace(request.Source)
	if request.Source == "" {
		request.Source = "admin"
	}
	if err := resolved.Validate(); err != nil {
		return runtimecfg.ApplyResult{}, &FieldError{Field: "runtime", Err: err}
	}
	return s.runtime.CommitRuntime(ctx, resolved, func() (config.Snapshot, error) {
		return s.settings.Update(ctx, settings.UpdateRequest{
			ExpectedVersion: request.ExpectedVersion, Runtime: resolved,
			Source: request.Source,
		})
	})
}

func (s *Service) ApplyResponse(ctx context.Context, result runtimecfg.ApplyResult) (ApplyResponse, error) {
	var secretStatus secret.Status
	if s.secrets != nil {
		var err error
		secretStatus, err = s.secrets.Status(ctx, result.Snapshot.Runtime.LLM.APIKeyRef)
		if err != nil {
			return ApplyResponse{}, err
		}
	}
	return ApplyResponse{Action: result.Action, Config: configResponse(result.Snapshot, result.Status, secretStatus)}, nil
}

func (s *Service) Rollback(ctx context.Context, request RollbackRequest) (runtimecfg.ApplyResult, error) {
	if s.settings == nil {
		return runtimecfg.ApplyResult{}, ErrUnavailable
	}
	runtimeConfig, err := s.settings.RuntimeAt(ctx, request.TargetVersion)
	if err != nil {
		return runtimecfg.ApplyResult{}, err
	}
	request.Source = strings.TrimSpace(request.Source)
	if request.Source == "" {
		request.Source = "admin:rollback"
	}
	return s.runtime.CommitRuntime(ctx, runtimeConfig, func() (config.Snapshot, error) {
		return s.settings.Rollback(ctx, request.TargetVersion, request.ExpectedVersion, request.Source)
	})
}

func (s *Service) RuntimeStatus() runtimecfg.Status {
	_, status := s.runtime.Snapshot()
	return status
}

func (s *Service) Restart(ctx context.Context, reason string) error {
	return s.runtime.Restart(ctx, strings.TrimSpace(reason))
}

func (s *Service) ReloadSearch(ctx context.Context) (searchcontract.ReloadResult, error) {
	return s.runtime.ReloadSearch(ctx, "")
}

func (s *Service) ScanReflection(ctx context.Context) (runtimecfg.ReflectionScanResult, error) {
	return s.runtime.ScanReflection(ctx)
}

func (s *Service) APIKeyStatus(ctx context.Context) (secret.Status, error) {
	if s.secrets == nil {
		return secret.Status{}, ErrUnavailable
	}
	snapshot, _ := s.runtime.Snapshot()
	return s.secrets.Status(ctx, snapshot.Runtime.LLM.APIKeyRef)
}

func (s *Service) ReplaceAPIKey(ctx context.Context, value string) (secret.Status, error) {
	if s.secrets == nil {
		return secret.Status{}, ErrUnavailable
	}
	snapshot, _ := s.runtime.Snapshot()
	return s.secrets.Replace(ctx, snapshot.Runtime.LLM.APIKeyRef, value)
}

func (s *Service) DeleteAPIKey(ctx context.Context) error {
	if s.secrets == nil {
		return ErrUnavailable
	}
	snapshot, _ := s.runtime.Snapshot()
	return s.secrets.Delete(ctx, snapshot.Runtime.LLM.APIKeyRef)
}
