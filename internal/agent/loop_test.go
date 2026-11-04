package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/tooling"
)

type modelFunc func(context.Context, llm.Request) (llm.Response, error)

func (modelFunc) Protocol() llm.Protocol { return llm.OpenAIResponses }
func (f modelFunc) Complete(ctx context.Context, r llm.Request) (llm.Response, error) {
	return f(ctx, r)
}
func (modelFunc) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}
func toolResponse(id, name, args string) llm.Response {
	return llm.Response{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: args}}}}
}
func loopRequest() llm.Request {
	return llm.Request{Model: "test", Messages: []llm.Message{llm.TextMessage(llm.RoleUser, "question")}, Tools: []llm.ToolDefinition{{Name: "lookup"}}}
}

func TestLoopUsesPriorResultToChooseNextCall(t *testing.T) {
	turn, calls := 0, 0
	loop := Loop{Client: modelFunc(func(_ context.Context, r llm.Request) (llm.Response, error) {
		turn++
		if turn == 1 {
			return toolResponse("list", "lookup", `{"list":true}`), nil
		}
		if turn == 2 {
			if r.Messages[2].ToolCallID != "list" || !strings.Contains(r.Messages[2].Text(), "item-7") {
				t.Fatal("list result missing", r.Messages)
			}
			return toolResponse("detail", "lookup", `{"id":"item-7"}`), nil
		}
		if len(r.Messages) != 5 || r.Messages[4].ToolCallID != "detail" || !strings.Contains(r.Messages[4].Text(), "details") {
			t.Fatal("detail result missing", r.Messages)
		}
		return llm.Response{Text: "final"}, nil
	}), Execute: func(_ context.Context, c tooling.Call) (tooling.Result, error) {
		calls++
		if calls == 1 {
			return tooling.Result{OK: true, Payload: "item-7"}, nil
		}
		if c.Arguments != `{"id":"item-7"}` {
			t.Fatal("dependent query lost")
		}
		return tooling.Result{OK: true, Payload: "details"}, nil
	}}
	response, err := loop.Run(context.Background(), loopRequest())
	if err != nil || response.Text != "final" || calls != 2 || turn != 3 {
		t.Fatalf("%+v %v calls=%d turns=%d", response, err, calls, turn)
	}
}

func TestLoopBudgetStopsRepeatedToolsAndHidesToolsOnLastTurn(t *testing.T) {
	turn, calls := 0, 0
	loop := Loop{Limits: Limits{MaxTurns: 3}, Client: modelFunc(func(_ context.Context, r llm.Request) (llm.Response, error) {
		turn++
		if turn == 3 && len(r.Tools) != 0 {
			t.Fatal("last turn still advertises tools")
		}
		return toolResponse(fmt.Sprint(turn), "lookup", "{}"), nil
	}), Execute: func(context.Context, tooling.Call) (tooling.Result, error) {
		calls++
		return tooling.Result{OK: true}, nil
	}}
	_, err := loop.Run(context.Background(), loopRequest())
	if err == nil || operation.Describe(err).Code != operation.BudgetExhausted || calls != 2 || turn != 3 {
		t.Fatalf("%v calls=%d turn=%d", err, calls, turn)
	}
}

func TestLoopRejectsBadBatchBeforeAnyExecution(t *testing.T) {
	for _, kind := range []string{"missing-id", "duplicate", "too-many"} {
		t.Run(kind, func(t *testing.T) {
			response := toolResponse("id", "lookup", "{}")
			if kind == "missing-id" {
				response.Message.ToolCalls[0].ID = ""
			} else {
				response.Message.ToolCalls = append(response.Message.ToolCalls, response.Message.ToolCalls[0])
			}
			calls := 0
			limits := Limits{}
			if kind == "too-many" {
				limits.MaxToolsPerTurn = 1
			}
			loop := Loop{Limits: limits, Client: modelFunc(func(context.Context, llm.Request) (llm.Response, error) { return response, nil }), Execute: func(context.Context, tooling.Call) (tooling.Result, error) {
				calls++
				return tooling.Result{OK: true}, nil
			}}
			if _, err := loop.Run(context.Background(), loopRequest()); err == nil || calls != 0 {
				t.Fatalf("bad batch executed: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestLoopUnknownToolNeverRunsAndOversizedResultIsValidJSON(t *testing.T) {
	turn, calls := 0, 0
	loop := Loop{Limits: Limits{MaxResultBytes: 512}, Client: modelFunc(func(_ context.Context, r llm.Request) (llm.Response, error) {
		turn++
		if turn == 1 {
			return toolResponse("denied", "secret-tool", "{}"), nil
		}
		var previous tooling.Result
		if err := json.Unmarshal([]byte(r.Messages[len(r.Messages)-1].Text()), &previous); err != nil {
			t.Fatal(err)
		}
		if turn == 2 {
			if previous.Failure == nil || previous.Failure.Code != operation.PolicyDenied {
				t.Fatalf("unknown tool result %+v", previous)
			}
			return toolResponse("big", "lookup", "{}"), nil
		}
		if previous.Failure == nil || previous.Failure.Code != operation.BudgetExhausted || len(r.Messages[len(r.Messages)-1].Text()) > 512 {
			t.Fatal("oversized result not bounded", previous)
		}
		return llm.Response{Text: "done"}, nil
	}), Execute: func(context.Context, tooling.Call) (tooling.Result, error) {
		calls++
		return tooling.Result{OK: true, Payload: strings.Repeat("x", 2000)}, nil
	}}
	if _, err := loop.Run(context.Background(), loopRequest()); err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestLoopHonorsCancelAndContextBudget(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		loop := Loop{Limits: Limits{Timeout: time.Millisecond * 10}, Client: modelFunc(func(ctx context.Context, _ llm.Request) (llm.Response, error) {
			<-ctx.Done()
			return llm.Response{}, ctx.Err()
		})}
		if _, err := loop.Run(context.Background(), loopRequest()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
	t.Run("context", func(t *testing.T) {
		calls := 0
		loop := Loop{Limits: Limits{MaxContextBytes: 512, MaxResultBytes: 512}, Client: modelFunc(func(context.Context, llm.Request) (llm.Response, error) {
			return toolResponse("huge", "lookup", strings.Repeat("x", 1000)), nil
		}), Execute: func(context.Context, tooling.Call) (tooling.Result, error) { calls++; return tooling.Result{}, nil }}
		if _, err := loop.Run(context.Background(), loopRequest()); err == nil || operation.Describe(err).Code != operation.BudgetExhausted || calls != 0 {
			t.Fatalf("%v calls=%d", err, calls)
		}
	})
}

func TestLimitsJSONRejectsUnknownAndUsesDefaults(t *testing.T) {
	for _, raw := range []string{`{"max_turns":-1}`, `{"timeout":"-1s"}`, `{"typo":1}`, `null`, `{"max_turns":null}`} {
		var limits Limits
		if err := json.Unmarshal([]byte(raw), &limits); err == nil {
			t.Fatal("invalid limits accepted", raw)
		}
	}
	var defaults Limits
	if err := json.Unmarshal([]byte(`{}`), &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.MaxTurns != 6 || defaults.Timeout != 90*time.Second || defaults.MaxResultBytes != 64<<10 {
		t.Fatalf("defaults %+v", defaults)
	}
}

func TestLoopEnforcesPerToolAndTotalBudgetsAcrossRounds(t *testing.T) {
	for _, perTool := range []bool{true, false} {
		t.Run(fmt.Sprint(perTool), func(t *testing.T) {
			turn, calls := 0, 0
			loop := Loop{Limits: Limits{MaxToolCalls: 2}, ToolBudgets: map[string]int{}, Execute: func(context.Context, tooling.Call) (tooling.Result, error) {
				calls++
				return tooling.Result{OK: true}, nil
			}}
			if perTool {
				loop.ToolBudgets["lookup"] = 1
			}
			loop.Client = modelFunc(func(_ context.Context, r llm.Request) (llm.Response, error) {
				turn++
				if turn <= 2 {
					return toolResponse(fmt.Sprint(turn), "lookup", "{}"), nil
				}
				if perTool {
					var result tooling.Result
					if err := json.Unmarshal([]byte(r.Messages[len(r.Messages)-1].Text()), &result); err != nil || result.Code != "budget_exhausted" {
						t.Fatalf("per-tool budget: %+v %v", result, err)
					}
				}
				if len(r.Tools) != 0 {
					t.Fatal("exhausted tools still advertised")
				}
				return llm.Response{Text: "done"}, nil
			})
			if _, err := loop.Run(context.Background(), loopRequest()); err != nil {
				t.Fatal(err)
			}
			want := 2
			if perTool {
				want = 1
			}
			if calls != want {
				t.Fatalf("calls=%d want=%d", calls, want)
			}
		})
	}
}
