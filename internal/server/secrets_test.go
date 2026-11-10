package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/secret"
)

func TestReferencedSecretsRequireAuthAndNeverReturnPlaintext(t *testing.T) {
	f := newAdminFixture(t)
	path := "/v1/admin/secrets/review/token"
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		w := httptest.NewRecorder()
		f.server.server.Handler.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatal(method, w.Code)
		}
	}
	for _, body := range []string{`{"value":"secret-one","typo":true}`, `{"value":"secret-one"} {}`, `{"api_key":"secret-one"}`} {
		if w := f.request(t, http.MethodPut, path, body); w.Code != http.StatusBadRequest {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := f.request(t, http.MethodGet, path, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatal("invalid requests created a credential", w.Code, w.Body)
	}
	var version uint64
	for _, value := range []string{"secret-one", "secret-two"} {
		w := f.request(t, http.MethodPut, path, `{"value":"`+value+`"}`)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), value) {
			t.Fatal(w.Code, w.Body)
		}
		var status secret.Status
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || !status.Configured || status.Version <= version || status.UpdatedAt.IsZero() {
			t.Fatal("write did not return updated credential status", w.Body, err)
		}
		version = status.Version
	}
	w := f.request(t, http.MethodGet, path, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"configured":true`) || strings.Contains(w.Body.String(), "secret-two") {
		t.Fatal(w.Code, w.Body)
	}
	data, err := os.ReadFile(f.secretPath)
	if err != nil || strings.Contains(string(data), "secret-two") {
		t.Fatal("credential not encrypted", err)
	}
	if w := f.request(t, http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatal(w.Code, w.Body)
	}
	if w := f.request(t, http.MethodGet, path, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatal("deleted credential still configured", w.Code, w.Body)
	}
}
