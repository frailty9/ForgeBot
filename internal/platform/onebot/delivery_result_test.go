package onebot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSendTimeoutPreservesUnknownExecution(t *testing.T) {
	requests := 0
	s := &Sender{cfg: testConfig("http://localhost"), logger: slog.New(slog.NewTextHandler(io.Discard, nil)), client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, context.DeadlineExceeded
	})}}
	err := s.SendTo(context.Background(), "8", messaging.OutboundMessage{Text: "hello"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost cause: %v", err)
	}
	failure := operation.Describe(err)
	if failure.Code != operation.Timeout || failure.Effect != operation.EffectUnknown || requests != 1 {
		t.Fatalf("failure=%+v requests=%d", failure, requests)
	}
}

func TestSendMapsHTTPPermissionWithoutGuessingRetcode(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer api.Close()
	s := &Sender{cfg: testConfig(api.URL), client: api.Client(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	err := s.SendTo(context.Background(), "8", messaging.OutboundMessage{Text: "hello"})
	if got := operation.Describe(err); got.Code != operation.PermissionDenied {
		t.Fatalf("got %+v", got)
	}
	err = s.SendTo(context.Background(), "8", messaging.OutboundMessage{Text: "hello", Media: []messaging.Media{{Kind: messaging.MediaImage}}})
	if got := operation.Describe(err); got.Code != operation.Unsupported || got.Effect != operation.NotStarted {
		t.Fatalf("got %+v", got)
	}
}
