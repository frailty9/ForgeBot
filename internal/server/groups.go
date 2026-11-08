package server

import "net/http"

func (s *Server) adminGroupSupport(w http.ResponseWriter, r *http.Request) {
	result, err := s.admin.GroupSupport(r.Context(), r.URL.Query().Get("conversation_id"))
	if err != nil {
		s.writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
