package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGroupAdministrationRequiresAuthAndRemovedActionsAreGone(t *testing.T) {
	fixture := newAdminFixture(t)
	r := httptest.NewRequest(http.MethodGet, "/v1/admin/groups/support?conversation_id=local", nil)
	w := httptest.NewRecorder()
	fixture.server.server.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("anonymous group API accepted", w.Code)
	}
	if w := fixture.request(t, http.MethodGet, "/v1/admin/groups/operations", ""); w.Code != http.StatusNotFound {
		t.Fatal("removed operations API still registered", w.Code)
	}
	if w := fixture.request(t, http.MethodPost, "/v1/admin/groups/review", `{"id":"x","expected_revision":1}`); w.Code != http.StatusNotFound {
		t.Fatal("removed review API still registered", w.Code, w.Body)
	}
	// 公告与撤回已挂起，路由不应继续注册。
	for _, path := range []string{"/v1/admin/groups/announcements", "/v1/admin/groups/recall"} {
		if w := fixture.request(t, http.MethodPost, path, `{"id":"x","conversation_id":"local"}`); w.Code != http.StatusNotFound {
			t.Fatal("suspended group action still registered", path, w.Code)
		}
	}
}
