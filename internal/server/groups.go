package server

import (
	"net/http"
	"strconv"

	"github.com/soleapi/forgebot/internal/group"
)

func (s *Server) adminGroupSupport(w http.ResponseWriter, r *http.Request) {
	result, err := s.admin.GroupSupport(r.Context(), r.URL.Query().Get("conversation_id"))
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) adminGroupOperations(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := s.admin.GroupOperations(r.Context(), limit)
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (s *Server) adminGroupAnnouncement(w http.ResponseWriter, r *http.Request) {
	s.adminGroupAction(w, r, "announcement")
}
func (s *Server) adminGroupRecall(w http.ResponseWriter, r *http.Request) {
	s.adminGroupAction(w, r, "recall")
}
func (s *Server) adminGroupAction(w http.ResponseWriter, r *http.Request, kind string) {
	var request group.ActionRequest
	if err := decodeAdminJSON(w, r, &request); err != nil {
		s.writeAdminError(w, err)
		return
	}
	result, err := s.admin.PerformGroupAction(r.Context(), kind, request)
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
