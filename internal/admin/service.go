// Package admin adapts runtime, settings, secrets, search, and reflection
// operations to the HTTP management surface.
package admin

import (
	"context"
	"errors"
	"strings"

	"github.com/soleapi/forgebot/internal/catalog"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/operation"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/secret"
	"github.com/soleapi/forgebot/internal/settings"
)

var ErrUnavailable = operation.New(operation.Unavailable, "management capability is unavailable")

// Service is the management facade used by the HTTP server.
type Service struct {
	runtime  *runtimecfg.Manager
	settings Configuration
	secrets  *secret.Manager
}

// Configuration 是管理端实际使用的配置职责，不包含未使用的 Current。
type Configuration interface {
	settings.HistoryReader
	settings.Writer
}

// UpdateRequest is a complete runtime replacement guarded by expected_version.
type UpdateRequest struct {
	ExpectedVersion uint64               `json:"expected_version"`
	Runtime         config.RuntimeConfig `json:"runtime"`
	Source          string               `json:"source"`
	Force           bool                 `json:"force,omitempty"`
}

// RollbackRequest identifies a persisted runtime revision to restore.
type RollbackRequest struct {
	TargetVersion   uint64 `json:"target_version"`
	ExpectedVersion uint64 `json:"expected_version"`
	Source          string `json:"source"`
}

// New validates management dependencies and creates the facade.
func New(runtime *runtimecfg.Manager, store Configuration, secrets *secret.Manager) (*Service, error) {
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
	result, err := s.PublicSnapshot(ctx, snapshot, configured)
	if err != nil {
		return nil, err
	}
	result["runtime_status"] = status
	if configured {
		result["llm_api_key"] = secretStatus
	} else {
		result["llm_api_key"] = map[string]any{"configured": false}
	}
	return result, nil
}

// PublicSnapshot is the only management rendering path for a runtime snapshot.
// It delegates opaque module and connector options to their owning modules.
func (s *Service) PublicSnapshot(ctx context.Context, snapshot config.Snapshot, apiKeyConfigured bool) (map[string]any, error) {
	result := snapshot.Public(apiKeyConfigured)
	publicModules, err := s.runtime.PublicModules(snapshot.Runtime)
	if err != nil {
		return nil, err
	}
	publicPlatforms, err := s.runtime.PublicPlatforms(ctx, snapshot.Runtime)
	if err != nil {
		return nil, err
	}
	runtime := result["runtime"].(map[string]any)
	runtime["modules"] = publicModules
	runtime["platforms"] = publicPlatforms
	return result, nil
}

func (s *Service) History(ctx context.Context, limit int) ([]settings.RevisionMeta, error) {
	if s.settings == nil {
		return nil, ErrUnavailable
	}
	return s.settings.History(ctx, limit)
}

func (s *Service) Validate(runtime config.RuntimeConfig) error {
	return operation.Wrap("config.validate", operation.Invalid, operation.NotStarted, s.runtime.ValidateConfiguration(runtime))
}

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
func (s *Service) Capabilities(ctx context.Context) catalog.Snapshot {
	return s.runtime.Capabilities(ctx)
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

func (s *Service) decisionKeyRef() (string, error) {
	snapshot, _ := s.runtime.Snapshot()
	if s.secrets == nil || snapshot.Runtime.Decision.DM == nil {
		return "", operation.New(operation.NotConfigured, "decision model credential is not configured")
	}
	return snapshot.Runtime.Decision.DM.APIKeyRef, nil
}
func (s *Service) DecisionAPIKeyStatus(ctx context.Context) (secret.Status, error) {
	ref, err := s.decisionKeyRef()
	if err != nil {
		return secret.Status{}, err
	}
	return s.secrets.Status(ctx, ref)
}
func (s *Service) ReplaceDecisionAPIKey(ctx context.Context, value string) (secret.Status, error) {
	ref, err := s.decisionKeyRef()
	if err != nil {
		return secret.Status{}, err
	}
	return s.secrets.Replace(ctx, ref, value)
}
func (s *Service) DeleteDecisionAPIKey(ctx context.Context) error {
	ref, err := s.decisionKeyRef()
	if err != nil {
		return err
	}
	return s.secrets.Delete(ctx, ref)
}

// 按引用管理连接器/外部审批凭据，仍只提供状态和写入生命周期，无明文读取。
func (s *Service) SecretStatus(ctx context.Context, ref string) (secret.Status, error) {
	if s.secrets == nil {
		return secret.Status{}, ErrUnavailable
	}
	return s.secrets.Status(ctx, ref)
}
func (s *Service) ReplaceSecret(ctx context.Context, ref, value string) (secret.Status, error) {
	if s.secrets == nil {
		return secret.Status{}, ErrUnavailable
	}
	return s.secrets.Replace(ctx, ref, value)
}
func (s *Service) DeleteSecret(ctx context.Context, ref string) error {
	if s.secrets == nil {
		return ErrUnavailable
	}
	return s.secrets.Delete(ctx, ref)
}

func (s *Service) GroupSupport(ctx context.Context, id string) (group.Support, error) {
	return s.runtime.Support(ctx, id)
}
