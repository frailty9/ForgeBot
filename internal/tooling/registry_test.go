package tooling

import (
	"context"
	"testing"
)

type testTool struct{ calls int }

func (t *testTool) Definition() Definition { return Definition{Name: "allowed"} }
func (t *testTool) Execute(context.Context, Call) Result {
	t.calls++
	return Result{OK: true, Payload: "ok"}
}

func TestRegistryOnlyExecutesRegisteredTools(t *testing.T) {
	tool := &testTool{}
	registry := NewRegistry(tool)
	if result := registry.Execute(context.Background(), Call{Name: "unknown"}); result.Code != "tool_not_allowed" || tool.calls != 0 {
		t.Fatalf("result=%+v calls=%d", result, tool.calls)
	}
	if result := registry.Execute(context.Background(), Call{Name: "allowed"}); !result.OK || tool.calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, tool.calls)
	}
}

func TestStrictMultiExecutorRejectsDuplicateToolNames(t *testing.T) {
	first := NewRegistry(&testTool{})
	second := NewRegistry(&testTool{})
	if _, err := NewStrictMultiExecutor(first, second); err == nil {
		t.Fatal("duplicate tool providers were accepted")
	}
}
