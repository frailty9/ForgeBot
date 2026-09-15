// Package server exposes health, normalized test ingress, and the bearer-
// protected management API over one HTTP listener.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/admin"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/gate"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/settings"
)

const maxAdminBodyBytes = 1 << 20

type adminErrorEnvelope struct {
	Error adminErrorBody `json:"error"`
}

type adminErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// Engine is the inbound boundary used by HTTP ingress and platform adapters.
type Engine interface {
	HandleInbound(context.Context, messaging.InboundMessage) (platform.HandleResult, error)
	GateStats() gate.Stats
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
	mux.HandleFunc("GET /v1/gate/stats", s.gateStats)
	mux.HandleFunc("POST /v1/messages", s.messages)
	mux.Handle("GET /v1/admin/config", s.authorize(http.HandlerFunc(s.adminConfig)))
	mux.Handle("GET /v1/admin/config/history", s.authorize(http.HandlerFunc(s.adminHistory)))
	mux.Handle("POST /v1/admin/config/validate", s.authorize(http.HandlerFunc(s.adminValidate)))
	mux.Handle("PUT /v1/admin/config", s.authorize(http.HandlerFunc(s.adminUpdate)))
	mux.Handle("POST /v1/admin/config/rollback", s.authorize(http.HandlerFunc(s.adminRollback)))
	mux.Handle("GET /v1/admin/runtime", s.authorize(http.HandlerFunc(s.adminRuntime)))
	mux.Handle("POST /v1/admin/runtime/restart", s.authorize(http.HandlerFunc(s.adminRestart)))
	mux.Handle("POST /v1/admin/search/reload", s.authorize(http.HandlerFunc(s.adminSearchReload)))
	mux.Handle("POST /v1/admin/reflection/scan", s.authorize(http.HandlerFunc(s.adminReflectionScan)))
	mux.Handle("GET /v1/admin/llm/api-key", s.authorize(http.HandlerFunc(s.adminAPIKeyStatus)))
	mux.Handle("PUT /v1/admin/llm/api-key", s.authorize(http.HandlerFunc(s.adminAPIKeyReplace)))
	mux.Handle("DELETE /v1/admin/llm/api-key", s.authorize(http.HandlerFunc(s.adminAPIKeyDelete)))
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
			writeAdminError(w, http.StatusUnauthorized, "unauthorized", "administrator authentication failed", "")
			return
		}
		if s.admin == nil {
			writeAdminError(w, http.StatusServiceUnavailable, "management_unavailable", "management API unavailable", "")
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
	var request admin.ValidateRequest
	if err := decodeAdminJSON(w, r, &request); err != nil {
		s.writeAdminError(w, err)
		return
	}
	if err := s.admin.Validate(request.Runtime); err != nil {
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
	response, err := s.admin.ApplyResponse(r.Context(), result)
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
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
	response, err := s.admin.ApplyResponse(r.Context(), result)
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
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

func decodeAdminJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func (s *Server) writeAdminError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, settings.ErrVersionConflict) {
		status = http.StatusConflict
	}
	if errors.Is(err, settings.ErrVersionNotFound) {
		status = http.StatusNotFound
	}
	if errors.Is(err, admin.ErrUnavailable) {
		status = http.StatusServiceUnavailable
	}
	if errors.Is(err, searchcontract.ErrDisabled) || errors.Is(err, searchcontract.ErrNoIndex) {
		status = http.StatusServiceUnavailable
	}
	if errors.Is(err, runtimecfg.ErrReflectionDisabled) || errors.Is(err, runtimecfg.ErrReflectionUnavailable) {
		status = http.StatusServiceUnavailable
	}
	code := "invalid_request"
	if errors.Is(err, settings.ErrVersionConflict) {
		code = "version_conflict"
	}
	if errors.Is(err, settings.ErrVersionNotFound) {
		code = "version_not_found"
	}
	if errors.Is(err, admin.ErrUnavailable) {
		code = "management_unavailable"
	}
	field := ""
	var fieldError *admin.FieldError
	if errors.As(err, &fieldError) {
		code, field = "validation_failed", fieldError.Field
	}
	writeAdminError(w, status, code, err.Error(), field)
}

func writeAdminError(w http.ResponseWriter, status int, code, message, field string) {
	writeJSON(w, status, adminErrorEnvelope{Error: adminErrorBody{Code: code, Message: message, Field: field}})
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
		"config":     snapshot.Public(false),
		"gate_stats": s.engine.GateStats(),
		"status":     runtimeStatus,
	})
}

func (s *Server) gateStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.GateStats())
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
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "result": result})
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
