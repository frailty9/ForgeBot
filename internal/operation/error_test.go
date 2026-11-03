package operation

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestBoundaryKeepsCauseAndEffect(t *testing.T) {
	sentinel := New(Conflict, "revision changed")
	err := Wrap("config.update", Internal, NotStarted, fmt.Errorf("write: %w", sentinel))
	if !errors.Is(err, sentinel) {
		t.Fatal("lost domain identity")
	}
	got := Describe(err)
	if got.Code != Conflict || got.Effect != NotStarted {
		t.Fatalf("got %+v", got)
	}
	got.Code = Invalid
	if Describe(err).Code != Conflict {
		t.Fatal("description mutated error")
	}
}

func TestTimeoutDoesNotImplyNotExecuted(t *testing.T) {
	err := Wrap("message.send", Unavailable, EffectUnknown, context.DeadlineExceeded)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lost cancellation cause")
	}
	got := Describe(err)
	if got.Code != Timeout || got.Effect != EffectUnknown {
		t.Fatalf("got %+v", got)
	}
	if Describe(nil) != nil {
		t.Fatal("success classified as failure")
	}
	if Describe(errors.New("vendor said no")).Effect != EffectUnknown {
		t.Fatal("invented execution certainty")
	}
}

func TestHTTPClassification(t *testing.T) {
	for status, code := range map[int]Code{401: Unauthenticated, 403: PermissionDenied, 404: NotFound, 409: Conflict, 429: RateLimited, 503: Unavailable, 504: Timeout, 418: Unknown} {
		if got := HTTPCode(status); got != code {
			t.Errorf("%d: got %s want %s", status, got, code)
		}
	}
}
