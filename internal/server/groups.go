package server

import (
	"net/http"
	"strconv"
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
