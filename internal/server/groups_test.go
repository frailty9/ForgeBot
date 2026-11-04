package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGroupAdministrationRequiresAuthAndManualReviewIsRemoved(t *testing.T) {
	fixture := newAdminFixture(t)
	for _, path := range []string{"/v1/admin/groups/operations", "/v1/admin/groups/support?conversation_id=local"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		fixture.server.server.Handler.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatal("anonymous group API accepted", w.Code)
		}
	}
	if w := fixture.request(t, http.MethodPost, "/v1/admin/groups/review", `{"id":"x","expected_revision":1}`); w.Code != http.StatusNotFound {
		t.Fatal("removed review API still registered", w.Code, w.Body)
	}
	if w := fixture.request(t, http.MethodPost, "/v1/admin/groups/announcements", `{"id":"x","conversation_id":"local","text":"hello"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatal("unconfigured group store accepted", w.Code, w.Body)
	}
}
