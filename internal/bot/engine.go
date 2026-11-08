// Package bot contains the protocol-neutral conversation engine. It applies
// gating, builds multimodal LLM requests, executes allow-listed tools, and
// sends normalized replies through the platform boundary.
package bot

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/soleapi/forgebot/internal/admission"
	"github.com/soleapi/forgebot/internal/agent"
	"github.com/soleapi/forgebot/internal/conversation"
	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/tooling"
)

// Policy contains behavior shared by one engine generation.
type Policy struct {
	Name                  string
	SystemPrompt          string
	Model                 string
	Admission             AdmissionPolicy
	ExtensionInstructions string
	ImageUnderstanding    bool
	Loop                  agent.Limits
}

type AdmissionPolicy struct {
	DedupTTL time.Duration
	DedupMax int
}

// Dependencies are the replaceable boundaries required by Engine.
type Dependencies struct {
	Memory       *conversation.Memory
	Hooks        *hooks.Chain
	CloseHooks   func(context.Context) error
	Generation   uint64
	Sender       platform.Sender
	LLM          llm.Client
	Tools        tooling.Executor
	ToolBudgets  map[string]int
	Commands     []modules.CommandRegistration
	Participants []modules.ParticipantRegistration
	Policy       Policy
}

// Engine handles inbound messages for one immutable runtime generation.
type Engine struct {
	deps      Dependencies
	logger    *slog.Logger
	admission *admission.Checker
	lifetime  sync.RWMutex
	closing   atomic.Bool
	closeOnce sync.Once
	closed    chan struct{}
}

var runSequence atomic.Uint64

type runKey struct{}
type execution struct {
	info              hooks.Info
	progressAttempted bool
	lease             *conversation.Lease
	history           []llm.Message
	finalText         string
}

var errEmptyInput = errors.New("empty_input")
var errModuleStopped = errors.New("module_stopped")
var errReplySuppressed = errors.New("reply_suppressed")
var errHookFailed = errors.New("hook_failed")

func (e *Engine) beginRun(ctx context.Context, in messaging.InboundMessage, trigger string) context.Context {
	ctx = decision.WithBudget(ctx, 2)
	return context.WithValue(ctx, runKey{}, &execution{info: hooks.Info{ProactiveRevision: in.ProactiveRevision, RunID: fmt.Sprintf("run-%d", runSequence.Add(1)), Generation: e.deps.Generation, ConversationID: in.ConversationID, UserID: in.UserID, MessageID: in.MessageID, Trigger: trigger}})
}
func runFrom(ctx context.Context) *execution {
	run, _ := ctx.Value(runKey{}).(*execution)
	if run == nil {
		return &execution{}
	}
	return run
}

// NewEngine validates dependencies and creates one immutable bot generation.
func NewEngine(ctx context.Context, deps Dependencies, logger *slog.Logger) (*Engine, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if deps.Sender == nil {
		return nil, fmt.Errorf("bot sender is required")
	}
	engine := &Engine{deps: deps, logger: logger, closed: make(chan struct{}), admission: admission.New(deps.Policy.Admission.DedupTTL, deps.Policy.Admission.DedupMax)}
	return engine, nil
}

// HandleInbound processes one normalized inbound message and returns a verdict
// describing whether it was handled, buffered, or rejected.
func (e *Engine) HandleInbound(ctx context.Context, msg messaging.InboundMessage) (platform.HandleResult, error) {
	ctx, cancel := context.WithTimeout(ctx, e.deps.Policy.Loop.Defaults().Timeout)
	defer cancel()
	e.lifetime.RLock()
	defer e.lifetime.RUnlock()
	if e.closing.Load() {
		return platform.HandleResult{Verdict: "stopped"}, operation.New(operation.Unavailable, "engine is stopped")
	}
	if err := ctx.Err(); err != nil {
		return platform.HandleResult{Verdict: "canceled"}, err
	}
	ctx = e.beginRun(ctx, msg, hooks.TriggerDirect)
	e.logger.DebugContext(ctx, "bot inbound message", "conversation_id", msg.ConversationID, "message_id", msg.MessageID, "user_id", msg.UserID, "mentioned", msg.Mentioned, "text_len", len([]rune(msg.Text)))
	if err := validateInbound(msg); err != nil {
		return platform.HandleResult{Accepted: false, Verdict: "invalid"}, operation.Wrap("message.validate", operation.Invalid, operation.NotStarted, err)
	}
	if e.admission.Check(msg) == admission.Duplicate {
		return platform.HandleResult{Accepted: false, Verdict: VerdictDuplicate.String()}, nil
	}
	if result, handled, err := e.handleModuleCommand(ctx, msg); handled {
		return result, err
	}
	for _, participant := range e.deps.Participants {
		result, err := participant.Handle(ctx, msg, e.respondDeferred)
		if err != nil {
			return platform.HandleResult{Accepted: false, Verdict: "participant_failed"}, err
		}
		if result.Consume {
			return platform.HandleResult{Accepted: result.Accepted, Verdict: result.Verdict}, nil
		}
	}
	if !msg.Addressed() {
		return platform.HandleResult{Accepted: false, Verdict: VerdictIgnored.String()}, nil
	}
	if e.deps.Memory != nil && (!containsImage(msg) || e.deps.Policy.ImageUnderstanding) {
		lease, err := e.deps.Memory.Acquire(ctx, msg.ConversationID, msg.UserID)
		if err != nil {
			return platform.HandleResult{Verdict: "conversation_unavailable"}, operation.Wrap("conversation.acquire", operation.Busy, operation.NotStarted, err)
		}
		defer lease.Release()
		runFrom(ctx).lease = lease
		// 决策和回答共享同一上下文快照，期间热更新不能让两者看到不同历史。
		runFrom(ctx).history = lease.History()
	}
	reply, err := e.replyText(ctx, msg)
	if err != nil {
		if errors.Is(err, errEmptyInput) || errors.Is(err, errModuleStopped) {
			return platform.HandleResult{Verdict: err.Error()}, nil
		}
		if errors.Is(err, errHookFailed) {
			return platform.HandleResult{Verdict: "hook_failed"}, err
		}
		e.logger.DebugContext(ctx, "bot reply generation failed", "conversation_id", msg.ConversationID, "message_id", msg.MessageID, "err", err)
		return platform.HandleResult{Accepted: false, Verdict: "llm_failed"}, operation.Wrap("answer.generate", operation.Unknown, operation.Describe(err).Effect, err)
	}
	e.logger.DebugContext(ctx, "bot reply generated", "conversation_id", msg.ConversationID, "message_id", msg.MessageID, "reply_len", len([]rune(reply)))
	if err := e.reply(ctx, msg, reply); err != nil {
		if errors.Is(err, errReplySuppressed) {
			return platform.HandleResult{Verdict: "reply_suppressed"}, nil
		}
		if errors.Is(err, errHookFailed) {
			return platform.HandleResult{Verdict: "hook_failed"}, err
		}
		e.logger.DebugContext(ctx, "bot reply delivery failed", "conversation_id", msg.ConversationID, "message_id", msg.MessageID, "err", err)
		return platform.HandleResult{Accepted: false, Verdict: "send_failed"}, err
	}
	run := runFrom(ctx)
	run.lease.Commit(userMessage(msg, msg.Text, false), run.finalText)
	return platform.HandleResult{Accepted: true, Verdict: VerdictPass.String()}, nil
}

func (e *Engine) handleModuleCommand(ctx context.Context, message messaging.InboundMessage) (platform.HandleResult, bool, error) {
	text := strings.TrimSpace(message.Text)
	if !strings.HasPrefix(text, "/") {
		return platform.HandleResult{}, false, nil
	}
	fields := strings.Fields(strings.TrimPrefix(text, "/"))
	if len(fields) == 0 {
		return platform.HandleResult{}, false, nil
	}
	name := strings.ToLower(fields[0])
	if index := strings.IndexByte(name, '@'); index >= 0 {
		name = name[:index]
	}
	for _, command := range e.deps.Commands {
		if strings.ToLower(command.Name) != name {
			continue
		}
		result, err := command.Handle(ctx, modules.CommandRequest{Name: name, Arguments: append([]string(nil), fields[1:]...), ConversationID: message.ConversationID, UserID: message.UserID, MessageID: message.MessageID, Instructions: e.instructions(message.PromptSuffix)})
		if err != nil {
			return platform.HandleResult{Verdict: "module_command_failed"}, true, err
		}
		if strings.TrimSpace(result.Reply) != "" {
			if err := e.reply(ctx, message, result.Reply); err != nil {
				return platform.HandleResult{Verdict: "send_failed"}, true, err
			}
		}
		return platform.HandleResult{Accepted: true, Verdict: "module_command"}, true, nil
	}
	return platform.HandleResult{}, false, nil
}

// Shutdown waits for active work and closes module and tool resources.
func (e *Engine) Shutdown(grace time.Duration) {
	e.closeOnce.Do(func() {
		e.closing.Store(true)
		go func() {
			e.lifetime.Lock()
			defer e.lifetime.Unlock()
			defer close(e.closed)
			if e.deps.CloseHooks != nil {
				closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if err := e.deps.CloseHooks(closeCtx); err != nil {
					e.logger.Warn("close modules failed", "err", err)
				}
				cancel()
			}
			if closer, ok := e.deps.Tools.(io.Closer); ok {
				if err := closer.Close(); err != nil {
					e.logger.Warn("close tool executor failed", "err", err)
				}
			}
		}()
	})
	if grace <= 0 {
		<-e.closed
		return
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-e.closed:
	case <-timer.C:
	}
}

func (e *Engine) Done() <-chan struct{} { return e.closed }
func (e *Engine) HasModel() bool        { return e.deps.LLM != nil }
func (e *Engine) HasSkills() bool {
	return strings.TrimSpace(e.deps.Policy.ExtensionInstructions) != ""
}
func (e *Engine) ToolNames() []string {
	definitions := e.toolDefinitions()
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

// SendEventReply 由宿主在已验证的群事件作用域调用，不向模块或模型暴露 Sender。
func (e *Engine) SendEventReply(ctx context.Context, id, text string) error {
	e.lifetime.RLock()
	defer e.lifetime.RUnlock()
	if e.closing.Load() {
		return operation.New(operation.Unavailable, "engine is stopped")
	}
	return e.deps.Sender.Send(ctx, messaging.OutboundMessage{ConversationID: id, Text: text})
}

func (e *Engine) respondDeferred(ctx context.Context, in messaging.InboundMessage) {
	ctx, cancel := context.WithTimeout(ctx, e.deps.Policy.Loop.Defaults().Timeout)
	defer cancel()
	ctx = e.beginRun(ctx, in, hooks.TriggerProactive)
	reply, err := e.replyText(ctx, in)
	if err != nil {
		if errors.Is(err, errModuleStopped) || errors.Is(err, errEmptyInput) {
			return
		}
		e.logger.ErrorContext(ctx, "generate proactive reply failed", "err", err)
		return
	}
	if err := e.reply(ctx, in, reply); err != nil && !errors.Is(err, errReplySuppressed) {
		e.logger.ErrorContext(ctx, "send proactive reply failed", "err", err)
	}
}

func (e *Engine) reply(ctx context.Context, in messaging.InboundMessage, text string) error {
	return e.deliver(ctx, in, text, "final")
}

func (e *Engine) deliver(ctx context.Context, in messaging.InboundMessage, text, kind string) error {
	result, err := e.deps.Hooks.Reply(ctx, runFrom(ctx).info, hooks.ReplyInput{Text: text, Kind: kind})
	if err != nil {
		return fmt.Errorf("%w: %w", errHookFailed, err)
	}
	if result.Suppress {
		return errReplySuppressed
	}
	if result.Text != nil {
		text = *result.Text
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return errReplySuppressed
	}
	message := messaging.OutboundMessage{
		ConversationID:   in.ConversationID,
		ReplyToMessageID: in.MessageID,
		Text:             text,
	}
	err = e.deps.Sender.Send(ctx, message)
	if err == nil {
		if kind == "final" {
			runFrom(ctx).finalText = text
		}
		return nil
	}
	return operation.Wrap("message.send", operation.Unknown, operation.Describe(err).Effect, err)
}

func (e *Engine) replyText(ctx context.Context, in messaging.InboundMessage) (string, error) {
	text := strings.TrimSpace(in.Text)
	hadCurrentText := text != ""
	instructions := e.instructions(in.PromptSuffix)
	images, mediaCount := usableMediaCounts(in)
	// 能力声明在任何输入 Hook/决策/工具前处理；未寻址消息不会进入本路径。
	if containsImage(in) && !e.deps.Policy.ImageUnderstanding {
		return "当前暂不支持图片", nil
	}
	hasQuotedText := in.Quoted != nil && strings.TrimSpace(in.Quoted.Text) != ""
	prepared, err := e.deps.Hooks.Prepare(ctx, runFrom(ctx).info, hooks.Input{
		Text: text, Mentioned: in.Mentioned, HasQuotedText: hasQuotedText,
		Images: images, MediaCount: mediaCount,
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", errHookFailed, err)
	}
	if prepared.Stop {
		return "", errModuleStopped
	}
	if prepared.Text != nil {
		text = strings.TrimSpace(*prepared.Text)
	}
	if text == "" && images == 0 && !hasQuotedText {
		return "", errEmptyInput
	}
	user := userMessage(in, text, !hadCurrentText && text != "")
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if e.deps.LLM == nil {
		return "", operation.New(operation.NotConfigured, "answer LLM is not configured")
	}
	// 主动插话已由模块决策放行，但回答模型仍可能判断不该回复；空最终文本
	// 会在投递时被抑制，避免对范围外问题主动插一句拒答。
	if runFrom(ctx).info.Trigger == hooks.TriggerProactive {
		instructions += "\n\n[宿主提示] 本次是未被 @ 的主动回复。若问题不在你的服务范围内，或不需要你回答，请只输出空内容。"
	}
	e.logger.DebugContext(ctx, "llm completion started", "model", e.deps.Policy.Model, "text_len", len([]rune(text)), "media_count", len(in.Media), "tools", len(e.toolDefinitions()))
	startedAt := time.Now()
	history := runFrom(ctx).history
	if !e.deps.Policy.ImageUnderstanding {
		for i := range history {
			for j := range history[i].Content {
				if history[i].Content[j].Type == "image" {
					history[i].Content[j] = llm.ContentPart{Type: "text", Text: "[此前图片当前不可用]"}
				}
			}
		}
	}
	response, err := (agent.Loop{Client: e.deps.LLM, Limits: e.deps.Policy.Loop, ToolBudgets: e.deps.ToolBudgets, Execute: func(ctx context.Context, call tooling.Call) (tooling.Result, error) {
		return e.executeTool(ctx, in, call)
	}}).Run(ctx, llm.Request{
		Model:        e.deps.Policy.Model,
		Instructions: instructions,
		Messages:     append(history, user),
		Tools:        e.toolDefinitions(),
	})
	if err != nil {
		e.logger.DebugContext(ctx, "llm completion failed", "model", e.deps.Policy.Model, "duration", time.Since(startedAt), "err", err)
		return "", err
	}
	e.logger.DebugContext(ctx, "llm completion finished", "model", e.deps.Policy.Model, "reply_len", len([]rune(response.Text)), "tool_calls", len(response.Message.ToolCalls), "total_tokens", response.Usage.TotalTokens)
	return response.Text, nil
}

// 所有轮次复用同一宿主工具入口，模块 Hook 不因进入后续轮次而被绕过。
func (e *Engine) executeTool(ctx context.Context, in messaging.InboundMessage, call tooling.Call) (tooling.Result, error) {
	run := runFrom(ctx)
	decision, err := e.deps.Hooks.Tool(ctx, run.info, hooks.ToolInput{Name: call.Name, Arguments: call.Arguments})
	if err != nil {
		return tooling.Result{}, fmt.Errorf("%w: %w", errHookFailed, err)
	}
	if decision.Deny {
		return tooling.Result{Code: tooling.CodeModuleDenied}, nil
	}
	if decision.Progress != nil && !run.progressAttempted {
		run.progressAttempted = true
		if err := e.deliver(ctx, in, decision.Progress.Text, "progress"); err != nil && !errors.Is(err, errReplySuppressed) {
			if errors.Is(err, errHookFailed) {
				return tooling.Result{}, err
			}
			e.logger.WarnContext(ctx, "send progress reply failed", "run_id", run.info.RunID, "err", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return tooling.Result{}, err
	}
	return e.deps.Tools.Execute(ctx, call), nil
}

func userMessage(in messaging.InboundMessage, currentText string, syntheticText bool) llm.Message {
	capacity := len(in.Media) + 1
	if in.Quoted != nil {
		capacity += len(in.Quoted.Media) + 1
	}
	parts := make([]llm.ContentPart, 0, capacity)
	if in.HasQuotedContent() {
		quotedText := strings.TrimSpace(in.Quoted.Text)
		label := "Quoted message:"
		if quotedText != "" {
			label += "\n" + quotedText
		}
		parts = append(parts, llm.ContentPart{Type: "text", Text: label})
		parts = appendImageParts(parts, in.Quoted.Media)
	}
	currentText = strings.TrimSpace(currentText)
	if currentText != "" && !syntheticText {
		if in.HasQuotedContent() {
			currentText = "Current message:\n" + currentText
		}
		parts = append(parts, llm.ContentPart{Type: "text", Text: currentText})
	}
	parts = appendImageParts(parts, in.Media)
	if currentText != "" && syntheticText {
		if in.HasQuotedContent() {
			currentText = "Current message:\n" + currentText
		}
		parts = append(parts, llm.ContentPart{Type: "text", Text: currentText})
	}
	return llm.Message{Role: llm.RoleUser, Content: parts}
}

func containsImage(in messaging.InboundMessage) bool {
	for _, media := range in.Media {
		if media.Kind == messaging.MediaImage {
			return true
		}
	}
	if in.Quoted != nil {
		for _, media := range in.Quoted.Media {
			if media.Kind == messaging.MediaImage {
				return true
			}
		}
	}
	return false
}

func usableMediaCounts(in messaging.InboundMessage) (images, total int) {
	total = len(in.Media)
	for _, media := range in.Media {
		if _, ok := imagePart(media); ok {
			images++
		}
	}
	if in.Quoted != nil {
		total += len(in.Quoted.Media)
		for _, media := range in.Quoted.Media {
			if _, ok := imagePart(media); ok {
				images++
			}
		}
	}
	return images, total
}

func appendImageParts(parts []llm.ContentPart, mediaItems []messaging.Media) []llm.ContentPart {
	for _, media := range mediaItems {
		if part, ok := imagePart(media); ok {
			parts = append(parts, part)
		}
	}
	return parts
}

func imagePart(media messaging.Media) (llm.ContentPart, bool) {
	if media.Kind != messaging.MediaImage {
		return llm.ContentPart{}, false
	}
	part := llm.ContentPart{Type: "image", MediaURL: strings.TrimSpace(media.URL), MimeType: strings.TrimSpace(media.MimeType)}
	if len(media.Data) > 0 {
		if part.MimeType == "" {
			part.MimeType = "image/jpeg"
		}
		part.Data = base64.StdEncoding.EncodeToString(media.Data)
	}
	return part, part.MediaURL != "" || part.Data != ""
}

func (e *Engine) instructions(suffix string) string {
	base := strings.TrimSpace(e.deps.Policy.SystemPrompt)
	if extension := strings.TrimSpace(e.deps.Policy.ExtensionInstructions); extension != "" {
		if base == "" {
			base = extension
		} else {
			base += "\n\n" + extension
		}
	}
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return base
	}
	if base == "" {
		return suffix
	}
	return base + "\n\n" + suffix
}

func (e *Engine) toolDefinitions() []llm.ToolDefinition {
	if e.deps.Tools == nil {
		return nil
	}
	definitions := e.deps.Tools.Definitions()
	result := make([]llm.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, llm.ToolDefinition{Name: definition.Name, Description: definition.Description, InputSchema: definition.InputSchema})
	}
	return result
}

func validateInbound(msg messaging.InboundMessage) error {
	if strings.TrimSpace(msg.ConversationID) == "" {
		return fmt.Errorf("conversation_id is required")
	}
	if strings.TrimSpace(msg.UserID) == "" {
		return fmt.Errorf("user_id is required")
	}
	if strings.TrimSpace(msg.MessageID) == "" {
		return fmt.Errorf("message_id is required")
	}
	if strings.TrimSpace(msg.Text) == "" && len(msg.Media) == 0 && !msg.HasQuotedContent() && !msg.Mentioned {
		return fmt.Errorf("text or media is required")
	}
	return nil
}

type Verdict string

const (
	VerdictPass      Verdict = "pass"
	VerdictAbsorbed  Verdict = "absorbed"
	VerdictDuplicate Verdict = "duplicate"
	VerdictIgnored   Verdict = "ignored"
)

func (v Verdict) String() string {
	return string(v)
}
