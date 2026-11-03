package tooling

import (
	"context"
	"testing"

	"github.com/soleapi/forgebot/internal/operation"
)

func TestResultSeparatesEmptySuccessAndFailure(t *testing.T) {
	empty := Normalize(Result{OK: true, Code: "no_results"})
	if empty.Failure != nil {
		t.Fatal("empty success treated as failure")
	}
	denied := Normalize(Result{Code: "tool_not_allowed"})
	if denied.Failure.Code != operation.PolicyDenied || denied.Failure.Effect != operation.NotStarted {
		t.Fatalf("got %+v", denied)
	}
	unknown := Normalize(Result{Code: "plugin_failed"})
	if unknown.Failure.Effect != operation.EffectUnknown {
		t.Fatal("invented plugin execution state")
	}
	timeout := Failed("mcp_call_failed", context.DeadlineExceeded)
	if timeout.Failure.Code != operation.Timeout || timeout.Failure.Effect != operation.EffectUnknown {
		t.Fatalf("got %+v", timeout)
	}
}
