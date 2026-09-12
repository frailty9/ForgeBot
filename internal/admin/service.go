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

type Service struct {
	runtime  *runtimecfg.Manager
	settings settings.Store
	secrets  *secret.Manager
}

type UpdateRequest struct {
	ExpectedVersion uint64               `json:"expected_version"`
	Runtime         config.RuntimeConfig `json:"runtime"`
	Source          string               `json:"source"`
	Force           bool                 `json:"force,omitempty"`
}

type RollbackRequest struct {
	TargetVersion   uint64 `json:"target_version"`
	ExpectedVersion uint64 `json:"expected_version"`
	Source          string `json:"source"`
}

func New(runtime *runtimecfg.Manager, store settings.Store, secrets *secret.Manager) (*Service, error) {
	if runtime == nil {
		return nil, errors.New("runtime manager is required")
	}
	return &Service{runtime: runtime, settings: store, secrets: secrets}, nil
}

func (s *Service) Config(ctx context.Context) (map[string]any, error) {
	snapshot, status := s.runtime.Snapshot()
	configured := false
	var secretStatus secret.Status
	if s.secrets != nil {
		var err error
		secretStatus, err = s.secrets.Status(ctx, snapshot.Runtime.LLM.APIKeyRef)
		if err != nil {
			return nil, err
		}
		configured = secretStatus.Configured
	}
	result := snapshot.Public(configured)
	result["runtime_status"] = status
	if configured {
		result["llm_api_key"] = secretStatus
	} else {
		result["llm_api_key"] = map[string]any{"configured": false}
	}
	return result, nil
}

func (s *Service) History(ctx context.Context, limit int) ([]settings.RevisionMeta, error) {
	if s.settings == nil {
		return nil, ErrUnavailable
	}
	return s.settings.History(ctx, limit)
}

func (s *Service) Validate(runtime config.RuntimeConfig) error { return runtime.Validate() }

func (s *Service) Update(ctx context.Context, request UpdateRequest) (runtimecfg.ApplyResult, error) {
	if s.settings == nil {
		return runtimecfg.ApplyResult{}, ErrUnavailable
	}
	request.Source = strings.TrimSpace(request.Source)
	if request.Source == "" {
		request.Source = "admin"
	}
	return s.runtime.CommitRuntime(ctx, request.Runtime, func() (config.Snapshot, error) {
		return s.settings.Update(ctx, settings.UpdateRequest{
			ExpectedVersion: request.ExpectedVersion, Runtime: request.Runtime,
			Source: request.Source, Force: request.Force,
		})
	})
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
