package hooks

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func inputMount(id string, source Source, order int, handle func(context.Context, Info, Input) (InputResult, error)) Mount {
	return Mount{ModuleID: id, Source: source, Order: order, Registrations: Registrations{AgentInput: []Registration[Input, InputResult]{{ID: "input", Handle: handle}}}}
}

func TestChainStableOrderAndCopies(t *testing.T) {
	var order []string
	makeHandler := func(id string) func(context.Context, Info, Input) (InputResult, error) {
		return func(_ context.Context, info Info, input Input) (InputResult, error) {
			order = append(order, id)
			info.UserID = "changed"
			input.Text += id
			return InputResult{Text: &input.Text}, nil
		}
	}
	mounts := []Mount{inputMount("external", External, -1000, makeHandler("e")), inputMount("b", Native, 10, makeHandler("b")), inputMount("a", Native, 10, makeHandler("a"))}
	chain, err := Compile(mounts, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	mounts[2].Registrations.AgentInput[0].Handle = nil
	info := Info{UserID: "original"}
	input := Input{Text: "start"}
	result, err := chain.Prepare(context.Background(), info, input)
	if err != nil || result.Text == nil || *result.Text != "startabe" || !reflect.DeepEqual(order, []string{"a", "b", "e"}) {
		t.Fatalf("result=%+v order=%v err=%v", result, order, err)
	}
	if info.UserID != "original" || input.Text != "start" {
		t.Fatal("input was mutated")
	}
}

func TestHookFailureAndTerminalSemantics(t *testing.T) {
	for _, scenario := range []string{"error", "panic", "abort", "invalid", "stop", "deadline", "parent_cancel"} {
		t.Run(scenario, func(t *testing.T) {
			later := false
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failing := inputMount("a", Native, 0, func(ctx context.Context, _ Info, input Input) (InputResult, error) {
				text := "must not leak"
				switch scenario {
				case "panic":
					panic("private panic payload")
				case "invalid":
					return InputResult{Text: &text, Stop: true}, nil
				case "stop":
					return InputResult{Stop: true}, nil
				case "deadline":
					<-ctx.Done()
					return InputResult{Text: &text}, nil
				case "parent_cancel":
					cancel()
					return InputResult{Text: &text}, nil
				}
				return InputResult{Text: &text}, errors.New("failed")
			})
			if scenario == "abort" {
				failing.Failure = Abort
			}
			chain, err := Compile([]Mount{failing, inputMount("b", Native, 0, func(_ context.Context, _ Info, input Input) (InputResult, error) {
				later = true
				if input.Text != "original" {
					t.Fatal("failed patch applied")
				}
				return InputResult{}, nil
			})}, quietLogger())
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Prepare(ctx, Info{}, Input{Text: "original"})
			wantLater := scenario == "error" || scenario == "panic" || scenario == "deadline"
			if later != wantLater {
				t.Fatalf("later=%v err=%v", later, err)
			}
			if scenario == "invalid" && !errors.Is(err, ErrInvalidResult) {
				t.Fatal(err)
			}
			if scenario == "parent_cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if scenario == "abort" && err == nil {
				t.Fatal("abort swallowed")
			}
			if scenario == "stop" && (!result.Stop || err != nil) {
				t.Fatal("stop lost")
			}
			if result.Text != nil {
				t.Fatal("failed result leaked")
			}
		})
	}
}

func TestToolDenialDiscardsEarlierProgress(t *testing.T) {
	later := false
	first := Mount{ModuleID: "a", Source: Native, Registrations: Registrations{BeforeTool: []Registration[ToolInput, ToolResult]{{ID: "progress", Handle: func(context.Context, Info, ToolInput) (ToolResult, error) {
		return ToolResult{Progress: &ProgressEffect{Text: "searching"}}, nil
	}}}}}
	second := Mount{ModuleID: "b", Source: External, Registrations: Registrations{BeforeTool: []Registration[ToolInput, ToolResult]{{ID: "deny", Handle: func(context.Context, Info, ToolInput) (ToolResult, error) { return ToolResult{Deny: true}, nil }}, {ID: "zzz", Handle: func(context.Context, Info, ToolInput) (ToolResult, error) { later = true; return ToolResult{}, nil }}}}}
	chain, err := Compile([]Mount{first, second}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Tool(context.Background(), Info{}, ToolInput{Name: "search_docs"})
	if err != nil || !result.Deny || result.Progress != nil || later {
		t.Fatalf("result=%+v err=%v later=%v", result, err, later)
	}
}

func TestReplyTransformSuppressAndInvalid(t *testing.T) {
	for _, mode := range []string{"transform", "suppress", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			text := "new"
			chain, err := Compile([]Mount{{ModuleID: "format", Source: External, Registrations: Registrations{BeforeReply: []Registration[ReplyInput, ReplyResult]{{ID: "format", Handle: func(context.Context, Info, ReplyInput) (ReplyResult, error) {
				if mode == "suppress" {
					return ReplyResult{Suppress: true}, nil
				}
				if mode == "invalid" {
					text = ""
				}
				return ReplyResult{Text: &text}, nil
			}}}}}}, quietLogger())
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Reply(context.Background(), Info{}, ReplyInput{Text: "old", Kind: "final"})
			if mode == "invalid" {
				if !errors.Is(err, ErrInvalidResult) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || mode == "suppress" && !result.Suppress || mode == "transform" && (result.Text == nil || *result.Text != "new") {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

func TestCompileRejectsInvalidRegistrations(t *testing.T) {
	handle := func(context.Context, Info, Input) (InputResult, error) { return InputResult{}, nil }
	valid := inputMount("valid", Native, 0, handle)
	invalidSource := valid
	invalidSource.Source = "core"
	duplicateHandler := inputMount("duplicate", Native, 0, handle)
	duplicateHandler.Registrations.AgentInput = append(duplicateHandler.Registrations.AgentInput, duplicateHandler.Registrations.AgentInput[0])
	many := inputMount("many", Native, 0, handle)
	for index := 0; index < MaxHandlers; index++ {
		many.Registrations.AgentInput = append(many.Registrations.AgentInput, Registration[Input, InputResult]{ID: strings.Repeat("x", index+1), Handle: handle})
	}
	for _, mounts := range [][]Mount{{valid, valid}, {invalidSource}, {duplicateHandler}, {inputMount("nil", Native, 0, nil)}, {many}} {
		if _, err := Compile(mounts, quietLogger()); err == nil {
			t.Fatal("invalid registration accepted")
		}
	}
}

func TestCanceledEmptyChain(t *testing.T) {
	var chain *Chain
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := chain.Prepare(ctx, Info{}, Input{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := chain.Tool(ctx, Info{}, ToolInput{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := chain.Reply(ctx, Info{}, ReplyInput{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestParentDeadlineStopsChain(t *testing.T) {
	chain, err := Compile([]Mount{inputMount("slow", Native, 0, func(ctx context.Context, _ Info, _ Input) (InputResult, error) {
		<-ctx.Done()
		return InputResult{}, nil
	})}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := chain.Prepare(ctx, Info{}, Input{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func BenchmarkEmptyChain(b *testing.B) {
	chain, _ := Compile(nil, quietLogger())
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = chain.Prepare(ctx, Info{}, Input{Text: "hello"})
	}
}
