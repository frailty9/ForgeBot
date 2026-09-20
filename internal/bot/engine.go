// Package bot contains the protocol-neutral conversation engine. It applies
// gating, builds multimodal LLM requests, executes allow-listed tools, and
// sends normalized replies through the platform boundary.
package bot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/soleapi/forgebot/internal/admission"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
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
}

type AdmissionPolicy struct {
	DedupTTL time.Duration
	DedupMax int
}

// Dependencies are the replaceable boundaries required by Engine.
type Dependencies struct {
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
}

var errEmptyInput = errors.New("empty_input")
var errModuleStopped = errors.New("module_stopped")
var errReplySuppressed = errors.New("reply_suppressed")
var errHookFailed = errors.New("hook_failed")

func (e *Engine) beginRun(ctx context.Context, in messaging.InboundMessage, trigger string) context.Context {
	return context.WithValue(ctx, runKey{}, &execution{info: hooks.Info{RunID: fmt.Sprintf("run-%d", runSequence.Add(1)), Generation: e.deps.Generation, ConversationID: in.ConversationID, UserID: in.UserID, MessageID: in.MessageID, Trigger: trigger}})
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
	e.lifetime.RLock()
	defer e.lifetime.RUnlock()
	if e.closing.Load() {
		return platform.HandleResult{Verdict: "stopped"}, errors.New("engine is stopped")
	}
	if err := ctx.Err(); err != nil {
		return platform.HandleResult{Verdict: "canceled"}, err
	}
	ctx = e.beginRun(ctx, msg, "direct")
	e.logger.DebugContext(ctx, "bot inbound message", "conversation_id", msg.ConversationID, "message_id", msg.MessageID, "user_id", msg.UserID, "mentioned", msg.Mentioned, "text_len", len([]rune(msg.Text)))
	if err := validateInbound(msg); err != nil {
		return platform.HandleResult{Accepted: false, Verdict: "invalid"}, err
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
	reply, err := e.replyText(ctx, msg)
	if err != nil {
		if errors.Is(err, errEmptyInput) || errors.Is(err, errModuleStopped) {
			return platform.HandleResult{Verdict: err.Error()}, nil
		}
		if errors.Is(err, errHookFailed) {
			return platform.HandleResult{Verdict: "hook_failed"}, err
		}
		e.logger.DebugContext(ctx, "bot reply generation failed", "conversation_id", msg.ConversationID, "message_id", msg.MessageID, "err", err)
		return platform.HandleResult{Accepted: false, Verdict: "llm_failed"}, err
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
		result, err := command.Handle(ctx, modules.CommandRequest{Name: name, Arguments: append([]string(nil), fields[1:]...), ConversationID: message.ConversationID, UserID: message.UserID, MessageID: message.MessageID, Administrator: message.Administrator, Instructions: e.instructions(message.PromptSuffix)})
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

func (e *Engine) respondDeferred(ctx context.Context, in messaging.InboundMessage) {
	ctx = e.beginRun(ctx, in, "proactive")
	reply, err := e.replyText(ctx, in)
	if err != nil {
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
	return e.deps.Sender.Send(ctx, message)
}

func (e *Engine) replyText(ctx context.Context, in messaging.InboundMessage) (string, error) {
	text := strings.TrimSpace(in.Text)
	hadCurrentText := text != ""
	instructions := e.instructions(in.PromptSuffix)
	images, mediaCount := usableMediaCounts(in)
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
		name := strings.TrimSpace(e.deps.Policy.Name)
		if name == "" {
			name = "ForgeBot"
		}
		if strings.TrimSpace(in.Text) == "" && images > 0 {
			text = ""
		}
		return name + " received: " + user.Text(), nil
	}
	e.logger.DebugContext(ctx, "llm completion started", "model", e.deps.Policy.Model, "text_len", len([]rune(text)), "media_count", len(in.Media), "tools", len(e.toolDefinitions()))
	messages := []llm.Message{user}
	tools := e.toolDefinitions()
	startedAt := time.Now()
	response, err := e.deps.LLM.Complete(ctx, llm.Request{
		Model:        e.deps.Policy.Model,
		Instructions: instructions,
		Messages:     messages,
		Tools:        tools,
	})
	if err != nil {
		e.logger.DebugContext(ctx, "llm completion failed", "model", e.deps.Policy.Model, "duration", time.Since(startedAt), "err", err)
		return "", err
	}
	e.logger.DebugContext(ctx, "llm completion finished", "model", e.deps.Policy.Model, "reply_len", len([]rune(response.Text)), "tool_calls", len(response.Message.ToolCalls), "total_tokens", response.Usage.TotalTokens)
	if len(response.Message.ToolCalls) == 0 {
		return response.Text, nil
	}
	messages = append(messages, llm.AssistantToolCallMessage(response.Message))
	used := make(map[string]int)
	allowed := make(map[string]bool, len(tools))
	for _, definition := range tools {
		allowed[definition.Name] = true
	}
	for _, call := range response.Message.ToolCalls {
		result := tooling.Result{Code: "budget_exhausted"}
		budget := e.deps.ToolBudgets[call.Name]
		if budget <= 0 {
			budget = 1
		}
		if used[call.Name] < budget && e.deps.Tools != nil {
			if !allowed[call.Name] {
				result = tooling.Result{Code: "tool_not_allowed"}
				payload, marshalErr := json.Marshal(result)
				if marshalErr != nil {
					payload = []byte(`{"ok":false,"code":"tool_result_failed"}`)
				}
				messages = append(messages, llm.ToolResultMessage(call.ID, call.Name, string(payload)))
				continue
			}
			used[call.Name]++
			run := runFrom(ctx)
			decision, hookErr := e.deps.Hooks.Tool(ctx, run.info, hooks.ToolInput{Name: call.Name, Arguments: call.Arguments})
			if hookErr != nil {
				return "", fmt.Errorf("%w: %w", errHookFailed, hookErr)
			}
			if decision.Deny {
				result = tooling.Result{Code: "module_denied"}
			} else {
				if decision.Progress != nil && !run.progressAttempted {
					run.progressAttempted = true
					if sendErr := e.deliver(ctx, in, decision.Progress.Text, "progress"); sendErr != nil && !errors.Is(sendErr, errReplySuppressed) {
						if errors.Is(sendErr, errHookFailed) {
							return "", sendErr
						}
						e.logger.WarnContext(ctx, "send progress reply failed", "run_id", run.info.RunID, "err", sendErr)
					}
				}
				if err := ctx.Err(); err != nil {
					return "", err
				}
				result = e.deps.Tools.Execute(ctx, tooling.Call{Name: call.Name, Arguments: call.Arguments})
			}
		}
		payload, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			payload = []byte(`{"ok":false,"code":"tool_result_failed"}`)
		}
		messages = append(messages, llm.ToolResultMessage(call.ID, call.Name, string(payload)))
	}
	startedAt = time.Now()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	final, err := e.deps.LLM.Complete(ctx, llm.Request{
		Model: e.deps.Policy.Model, Instructions: instructions, Messages: messages,
	})
	if err != nil {
		e.logger.DebugContext(ctx, "llm tool follow-up failed", "model", e.deps.Policy.Model, "duration", time.Since(startedAt), "err", err)
		return "", err
	}
	e.logger.DebugContext(ctx, "llm tool follow-up finished", "model", e.deps.Policy.Model, "reply_len", len([]rune(final.Text)), "total_tokens", final.Usage.TotalTokens)
	return final.Text, nil
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
	if strings.TrimSpace(msg.Text) == "" && len(msg.Media) == 0 && !msg.Mentioned {
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
