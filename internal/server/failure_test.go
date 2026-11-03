package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/settings"
)

func TestAdminErrorUsesStableClassification(t *testing.T) {
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, tc := range []struct {
		err    error
		status int
		code   operation.Code
	}{
		{settings.ErrVersionConflict, http.StatusConflict, operation.Conflict},
		{settings.ErrVersionNotFound, http.StatusNotFound, operation.NotFound},
		{errors.New("private vendor details"), http.StatusInternalServerError, operation.Unknown},
	} {
		recorder := httptest.NewRecorder()
		s.writeAdminError(recorder, tc.err)
		if recorder.Code != tc.status {
			t.Fatalf("got %d want %d: %s", recorder.Code, tc.status, recorder.Body)
		}
		if strings.Contains(recorder.Body.String(), "private vendor") {
			t.Fatal("diagnostic details leaked")
		}
		var response struct {
			Failure operation.Failure `json:"failure"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Failure.Code != tc.code {
			t.Fatalf("got %+v", response)
		}
	}
}
