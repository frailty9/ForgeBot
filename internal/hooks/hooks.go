// Package hooks defines bounded, typed extension points owned by the host.
package hooks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

type Stage string

const (
	AgentInput     Stage = "agent.input.prepare"
	BeforeTool     Stage = "tool.before_execute"
	BeforeReply    Stage = "reply.before_send"
	MaxHandlers          = 32
	MaxModules           = 64
	MaxTextBytes         = 64 << 10
	HandlerTimeout       = 200 * time.Millisecond
	ChainTimeout         = time.Second
)

type Source string

const (
	Native   Source = "native"
	External Source = "external"
)

type FailurePolicy string

const (
	Continue FailurePolicy = "continue"
	Abort    FailurePolicy = "abort"
)

var ErrInvalidResult = errors.New("invalid hook result")
var ErrPanic = errors.New("hook panicked")

// Info contains value-only attribution, not mutable host state or credentials.
type Info struct {
	RunID             string
	Generation        uint64
	ConversationID    string
	UserID            string
	MessageID         string
	Trigger           string
	ProactiveRevision uint64
}

type Input struct {
	Text          string
	Mentioned     bool
	HasQuotedText bool
	Images        int
	MediaCount    int
}
type InputResult struct {
	Text *string
	Stop bool
}
type ToolInput struct {
	Name      string
	Arguments string
}
type ToolResult struct {
	Deny     bool
	Progress *ProgressEffect
}
type ProgressEffect struct{ Text string }
type ReplyInput struct {
	Text string
	Kind string
}
type ReplyResult struct {
	Text     *string
	Suppress bool
}

type Registration[Input, Result any] struct {
	ID     string
	Handle func(context.Context, Info, Input) (Result, error)
}
type Registrations struct {
	AgentInput  []Registration[Input, InputResult]
	BeforeTool  []Registration[ToolInput, ToolResult]
	BeforeReply []Registration[ReplyInput, ReplyResult]
}

// Mount is assembled by the host, not accepted directly from plugin payloads.
type Mount struct {
	ModuleID      string
	Source        Source
	Order         int
	Failure       FailurePolicy
	Registrations Registrations
}
type Description struct {
	Hook      Stage  `json:"hook"`
	HandlerID string `json:"handler_id"`
}
type handler[Input, Result any] struct {
	module  string
	id      string
	failure FailurePolicy
	invoke  func(context.Context, Info, Input) (Result, error)
}
type Chain struct {
	input  []handler[Input, InputResult]
	tool   []handler[ToolInput, ToolResult]
	reply  []handler[ReplyInput, ReplyResult]
	logger *slog.Logger
}

func Describe(reg Registrations) []Description {
	result := make([]Description, 0, len(reg.AgentInput)+len(reg.BeforeTool)+len(reg.BeforeReply))
	for _, entry := range reg.AgentInput {
		result = append(result, Description{AgentInput, entry.ID})
	}
	for _, entry := range reg.BeforeTool {
		result = append(result, Description{BeforeTool, entry.ID})
	}
	for _, entry := range reg.BeforeReply {
		result = append(result, Description{BeforeReply, entry.ID})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Hook != result[right].Hook {
			return result[left].Hook < result[right].Hook
		}
		return result[left].HandlerID < result[right].HandlerID
	})
	return result
}

func Compile(mounts []Mount, logger *slog.Logger) (*Chain, error) {
	if len(mounts) > MaxModules {
		return nil, errors.New("too many hook modules")
	}
	if logger == nil {
		logger = slog.Default()
	}
	chain := &Chain{logger: logger}
	ordered := append([]Mount(nil), mounts...)
	sort.Slice(ordered, func(left, right int) bool {
		a, b := ordered[left], ordered[right]
		if a.Source != b.Source {
			return a.Source == Native
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.ModuleID < b.ModuleID
	})
	seen := make(map[string]bool)
	for _, mount := range ordered {
		if mount.ModuleID == "" || seen[mount.ModuleID] {
			return nil, errors.New("invalid or duplicate module mount")
		}
		seen[mount.ModuleID] = true
		if mount.Source != Native && mount.Source != External {
			return nil, errors.New("invalid hook source")
		}
		if mount.Failure == "" {
			mount.Failure = Continue
		}
		if mount.Failure != Continue && mount.Failure != Abort {
			return nil, errors.New("invalid failure policy")
		}
		ids := make(map[string]bool)
		for _, entry := range Describe(mount.Registrations) {
			if strings.TrimSpace(entry.HandlerID) == "" || ids[entry.HandlerID] {
				return nil, fmt.Errorf("module %s has duplicate or empty handler id", mount.ModuleID)
			}
			ids[entry.HandlerID] = true
		}
		var err error
		chain.input, err = add(chain.input, mount, mount.Registrations.AgentInput)
		if err != nil {
			return nil, err
		}
		chain.tool, err = add(chain.tool, mount, mount.Registrations.BeforeTool)
		if err != nil {
			return nil, err
		}
		chain.reply, err = add(chain.reply, mount, mount.Registrations.BeforeReply)
		if err != nil {
			return nil, err
		}
	}
	return chain, nil
}

func add[Input, Result any](list []handler[Input, Result], mount Mount, entries []Registration[Input, Result]) ([]handler[Input, Result], error) {
	ordered := append([]Registration[Input, Result](nil), entries...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	for _, entry := range ordered {
		if entry.Handle == nil {
			return nil, errors.New("nil hook handler")
		}
		list = append(list, handler[Input, Result]{mount.ModuleID, entry.ID, mount.Failure, entry.Handle})
	}
	if len(list) > MaxHandlers {
		return nil, errors.New("too many handlers for hook")
	}
	return list, nil
}

func call[Input, Result any](ctx context.Context, logger *slog.Logger, stage Stage, entry handler[Input, Result], info Info, input Input) (result Result, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	started := time.Now()
	handlerCtx, cancel := context.WithTimeout(ctx, HandlerTimeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = ErrPanic
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if handlerCtx.Err() != nil {
			err = handlerCtx.Err()
		}
		outcome := "ok"
		level := slog.LevelDebug
		if err != nil {
			outcome = "failed"
			level = slog.LevelWarn
		}
		if logger.Enabled(ctx, level) {
			code := ""
			switch {
			case errors.Is(err, context.Canceled):
				code = "canceled"
			case errors.Is(err, context.DeadlineExceeded):
				code = "deadline_exceeded"
			case errors.Is(err, ErrPanic):
				code = "panic"
			case err != nil:
				code = "handler_failed"
			}
			logger.Log(ctx, level, "hook execution", "run_id", info.RunID, "generation", info.Generation, "module_id", entry.module, "handler_id", entry.id, "hook", stage, "duration", time.Since(started), "outcome", outcome, "error_code", code)
		}
	}()
	return entry.invoke(handlerCtx, info, input)
}

func fatal(ctx context.Context, failure FailurePolicy, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if failure == Abort {
		return err
	}
	return nil
}

func invalid(logger *slog.Logger, info Info, module, id string, stage Stage) error {
	logger.Warn("hook result rejected", "run_id", info.RunID, "generation", info.Generation, "module_id", module, "handler_id", id, "hook", stage, "outcome", "invalid")
	return ErrInvalidResult
}

func (c *Chain) Prepare(ctx context.Context, info Info, input Input) (InputResult, error) {
	if err := ctx.Err(); err != nil {
		return InputResult{}, err
	}
	if c == nil || len(c.input) == 0 {
		return InputResult{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, ChainTimeout)
	defer cancel()
	var result InputResult
	for _, entry := range c.input {
		next, err := call(ctx, c.logger, AgentInput, entry, info, input)
		if err != nil {
			if err = fatal(ctx, entry.failure, err); err != nil {
				return InputResult{}, err
			}
			continue
		}
		if next.Stop && next.Text != nil || next.Text != nil && len(*next.Text) > MaxTextBytes {
			return InputResult{}, invalid(c.logger, info, entry.module, entry.id, AgentInput)
		}
		if next.Stop {
			return next, nil
		}
		if next.Text != nil {
			text := *next.Text
			result.Text = &text
			input.Text = text
		}
	}
	return result, nil
}

func (c *Chain) Tool(ctx context.Context, info Info, input ToolInput) (ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	if c == nil || len(c.tool) == 0 {
		return ToolResult{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, ChainTimeout)
	defer cancel()
	var result ToolResult
	for _, entry := range c.tool {
		next, err := call(ctx, c.logger, BeforeTool, entry, info, input)
		if err != nil {
			if err = fatal(ctx, entry.failure, err); err != nil {
				return ToolResult{}, err
			}
			continue
		}
		if next.Deny && next.Progress != nil || next.Progress != nil && (strings.TrimSpace(next.Progress.Text) == "" || len(next.Progress.Text) > MaxTextBytes) {
			return ToolResult{}, invalid(c.logger, info, entry.module, entry.id, BeforeTool)
		}
		if next.Deny {
			return ToolResult{Deny: true}, nil
		}
		if next.Progress != nil && result.Progress == nil {
			effect := *next.Progress
			result.Progress = &effect
		}
	}
	return result, nil
}

func (c *Chain) Reply(ctx context.Context, info Info, input ReplyInput) (ReplyResult, error) {
	if err := ctx.Err(); err != nil {
		return ReplyResult{}, err
	}
	if c == nil || len(c.reply) == 0 {
		return ReplyResult{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, ChainTimeout)
	defer cancel()
	var result ReplyResult
	for _, entry := range c.reply {
		next, err := call(ctx, c.logger, BeforeReply, entry, info, input)
		if err != nil {
			if err = fatal(ctx, entry.failure, err); err != nil {
				return ReplyResult{}, err
			}
			continue
		}
		if next.Suppress && next.Text != nil || next.Text != nil && (strings.TrimSpace(*next.Text) == "" || len(*next.Text) > MaxTextBytes) {
			return ReplyResult{}, invalid(c.logger, info, entry.module, entry.id, BeforeReply)
		}
		if next.Suppress {
			return next, nil
		}
		if next.Text != nil {
			text := *next.Text
			result.Text = &text
			input.Text = text
		}
	}
	return result, nil
}
