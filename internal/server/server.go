// Package server exposes health, normalized test ingress, and the bearer-
// protected management API over one HTTP listener.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/admin"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/proactive"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
)

const maxAdminBodyBytes = 1 << 20

// Engine is the inbound boundary used by HTTP ingress and platform adapters.
type Engine interface {
	HandleInbound(context.Context, messaging.InboundMessage) (platform.HandleResult, error)
	ProactiveStats() proactive.Stats
}

// Server owns HTTP routes and delegates state changes to the admin facade.
type Server struct {
	cfg    config.Snapshot
	logger *slog.Logger
	engine Engine
	admin  *admin.Service
	server *http.Server
}

// New creates an HTTP server with the supplied runtime and management boundaries.
func New(cfg config.Snapshot, logger *slog.Logger, engine Engine, adminService *admin.Service) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, logger: logger, engine: engine, admin: adminService}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /v1/runtime", s.runtime)
	mux.HandleFunc("GET /v1/proactive/stats", s.proactiveStats)
	mux.HandleFunc("POST /v1/messages", s.messages)
	mux.Handle("GET /v1/admin/config", s.authorize(http.HandlerFunc(s.adminConfig)))
	mux.Handle("GET /v1/admin/config/history", s.authorize(http.HandlerFunc(s.adminHistory)))
	mux.Handle("POST /v1/admin/config/validate", s.authorize(http.HandlerFunc(s.adminValidate)))
	mux.Handle("PUT /v1/admin/config", s.authorize(http.HandlerFunc(s.adminUpdate)))
	mux.Handle("POST /v1/admin/config/rollback", s.authorize(http.HandlerFunc(s.adminRollback)))
	mux.Handle("GET /v1/admin/runtime", s.authorize(http.HandlerFunc(s.adminRuntime)))
	mux.Handle("GET /v1/admin/capabilities", s.authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.admin.Capabilities(r.Context()))
	})))
	mux.Handle("POST /v1/admin/runtime/restart", s.authorize(http.HandlerFunc(s.adminRestart)))
	mux.Handle("POST /v1/admin/search/reload", s.authorize(http.HandlerFunc(s.adminSearchReload)))
	mux.Handle("POST /v1/admin/reflection/scan", s.authorize(http.HandlerFunc(s.adminReflectionScan)))
	mux.Handle("GET /v1/admin/llm/api-key", s.authorize(http.HandlerFunc(s.adminAPIKeyStatus)))
	mux.Handle("PUT /v1/admin/llm/api-key", s.authorize(http.HandlerFunc(s.adminAPIKeyReplace)))
	mux.Handle("DELETE /v1/admin/llm/api-key", s.authorize(http.HandlerFunc(s.adminAPIKeyDelete)))
	mux.Handle("GET /v1/admin/decision/api-key", s.authorize(http.HandlerFunc(s.adminDecisionAPIKeyStatus)))
	mux.Handle("PUT /v1/admin/decision/api-key", s.authorize(http.HandlerFunc(s.adminDecisionAPIKeyReplace)))
	mux.Handle("DELETE /v1/admin/decision/api-key", s.authorize(http.HandlerFunc(s.adminDecisionAPIKeyDelete)))
	mux.Handle("GET /v1/admin/groups/support", s.authorize(http.HandlerFunc(s.adminGroupSupport)))
	s.server = &http.Server{
		Addr:         cfg.Static.Server.Addr,
		Handler:      requestLog(logger, mux),
		ReadTimeout:  cfg.Static.Server.ReadTimeout,
		WriteTimeout: cfg.Static.Server.WriteTimeout,
	}
	return s
}

func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(s.cfg.Static.AdminToken)
		authorization := r.Header.Get("Authorization")
		provided := ""
		if strings.HasPrefix(authorization, "Bearer ") {
			provided = strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
		}
		if token == "" || len(token) != len(provided) || subtle.ConstantTimeCompare([]byte(token), []byte(provided)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if s.admin == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "management API unavailable"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) adminConfig(w http.ResponseWriter, r *http.Request) {
	result, err := s.admin.Config(r.Context())
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) adminHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := s.admin.History(r.Context(), limit)
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) adminValidate(w http.ResponseWriter, r *http.Request) {
	var runtimeConfig config.RuntimeConfig
	if err := decodeAdminJSON(w, r, &runtimeConfig); err != nil {
		s.writeAdminError(w, err)
		return
	}
	if err := s.admin.Validate(runtimeConfig); err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"valid": true})
}

func (s *Server) adminUpdate(w http.ResponseWriter, r *http.Request) {
	var request admin.UpdateRequest
	if err := decodeAdminJSON(w, r, &request); err != nil {
		s.writeAdminError(w, err)
		return
	}
	result, err := s.admin.Update(r.Context(), request)
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	public, publicErr := s.admin.PublicSnapshot(r.Context(), result.Snapshot, false)
	if publicErr != nil {
		s.writeAdminError(w, publicErr)
		return
	}
	writeJSON(w, http.StatusOK, publicApplyResult(result, public))
}

func (s *Server) adminRollback(w http.ResponseWriter, r *http.Request) {
	var request admin.RollbackRequest
	if err := decodeAdminJSON(w, r, &request); err != nil {
		s.writeAdminError(w, err)
		return
	}
	result, err := s.admin.Rollback(r.Context(), request)
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	public, publicErr := s.admin.PublicSnapshot(r.Context(), result.Snapshot, false)
	if publicErr != nil {
		s.writeAdminError(w, publicErr)
		return
	}
	writeJSON(w, http.StatusOK, publicApplyResult(result, public))
}

func publicApplyResult(result runtimecfg.ApplyResult, public map[string]any) map[string]any {
	return map[string]any{
		"action": result.Action,
		"status": result.Status,
		"config": public,
	}
}

func (s *Server) adminRuntime(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.admin.RuntimeStatus())
}

func (s *Server) adminRestart(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Reason string `json:"reason"`
	}
	if err := decodeAdminJSON(w, r, &request); err != nil {
		s.writeAdminError(w, err)
		return
	}
	if err := s.admin.Restart(r.Context(), request.Reason); err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.admin.RuntimeStatus())
}

func (s *Server) adminSearchReload(w http.ResponseWriter, r *http.Request) {
	result, err := s.admin.ReloadSearch(r.Context())
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) adminReflectionScan(w http.ResponseWriter, r *http.Request) {
	result, err := s.admin.ScanReflection(r.Context())
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) adminAPIKeyStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.admin.APIKeyStatus(r.Context())
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) adminAPIKeyReplace(w http.ResponseWriter, r *http.Request) {
	var request struct {
		APIKey string `json:"api_key"`
	}
	if err := decodeAdminJSON(w, r, &request); err != nil {
		s.writeAdminError(w, err)
		return
	}
	status, err := s.admin.ReplaceAPIKey(r.Context(), request.APIKey)
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) adminAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.admin.DeleteAPIKey(r.Context()); err != nil {
		s.writeAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminDecisionAPIKeyStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.admin.DecisionAPIKeyStatus(r.Context())
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
func (s *Server) adminDecisionAPIKeyReplace(w http.ResponseWriter, r *http.Request) {
	var request struct {
		APIKey string `json:"api_key"`
	}
	if err := decodeAdminJSON(w, r, &request); err != nil {
		s.writeAdminError(w, err)
		return
	}
	status, err := s.admin.ReplaceDecisionAPIKey(r.Context(), request.APIKey)
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
func (s *Server) adminDecisionAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.admin.DeleteDecisionAPIKey(r.Context()); err != nil {
		s.writeAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeAdminJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return operation.Wrap("request.decode", operation.Invalid, operation.NotStarted, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return operation.New(operation.Invalid, "request body must contain one JSON value")
	}
	return nil
}

func (s *Server) writeAdminError(w http.ResponseWriter, err error) {
	s.logger.Error("management operation failed", "err", err, "failure", operation.Describe(err))
	failure := operation.Describe(err)
	writeJSON(w, failureStatus(failure.Code), map[string]any{"error": operation.PublicMessage(failure.Code), "failure": failure})
}

// HTTP 只负责领域分类到状态码的映射，不按错误文案猜测失败原因。
func failureStatus(code operation.Code) int {
	switch code {
	case operation.Invalid:
		return http.StatusBadRequest
	case operation.Unauthenticated:
		return http.StatusUnauthorized
	case operation.PermissionDenied, operation.PolicyDenied:
		return http.StatusForbidden
	case operation.NotFound:
		return http.StatusNotFound
	case operation.Expired:
		return http.StatusGone
	case operation.Conflict, operation.Busy:
		return http.StatusConflict
	case operation.RateLimited, operation.BudgetExhausted:
		return http.StatusTooManyRequests
	case operation.Unsupported:
		return http.StatusNotImplemented
	case operation.Disabled, operation.NotConfigured, operation.Unavailable:
		return http.StatusServiceUnavailable
	case operation.Timeout:
		return http.StatusGatewayTimeout
	case operation.Canceled:
		return http.StatusRequestTimeout
	default:
		return http.StatusInternalServerError
	}
}

func (s *Server) Start() error {
	s.logger.Info("starting http server", "addr", s.server.Addr)
	err := s.server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) runtime(w http.ResponseWriter, r *http.Request) {
	snapshot := s.cfg
	var runtimeStatus any
	if provider, ok := s.engine.(interface {
		Snapshot() (config.Snapshot, runtimecfg.Status)
	}); ok {
		snapshot, runtimeStatus = provider.Snapshot()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config":          snapshot.Public(false),
		"proactive_stats": s.engine.ProactiveStats(),
		"status":          runtimeStatus,
	})
}

func (s *Server) proactiveStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.ProactiveStats())
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	var msg messaging.InboundMessage
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&msg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := s.engine.HandleInbound(r.Context(), msg)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "message processing failed", "err", err)
		failure := operation.Describe(err)
		writeJSON(w, failureStatus(failure.Code), map[string]any{"error": operation.PublicMessage(failure.Code), "failure": failure, "result": result})
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.InfoContext(r.Context(), "http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}
