// Package bot contains the protocol-neutral conversation engine. It applies
// gating, builds multimodal LLM requests, executes allow-listed tools, and
// sends normalized replies through the platform boundary.
package bot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/gate"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/tooling"
)

// Policy contains behavior shared by one engine generation.
type Policy struct {
	Name                  string
	SystemPrompt          string
	Model                 string
	Gate                  GatePolicy
	GroupGates            map[string]GatePolicy
	Search                SearchPolicy
	ExtensionInstructions string
}

type SearchPolicy struct {
	Enabled           bool
	DefaultResults    int
	MaxResults        int
	MaxQueriesPerTurn int
	MaxContextChars   int
}

type GatePolicy struct {
	Enabled              bool
	AutoReplyUnmentioned bool
	Silence              time.Duration
	MaxCount             int
	MaxWindow            time.Duration
	DedupTTL             time.Duration
	DedupMax             int
	MaxBuffered          int
	MaxTextLen           int
	MaxConcurrent        int
}

// Dependencies are the replaceable boundaries required by Engine.
type Dependencies struct {
	Sender    platform.Sender
	LLM       llm.Client
	Retriever searchcontract.Retriever
	Tools     tooling.Executor
	Policy    Policy
}

// Engine handles inbound messages for one immutable runtime generation.
type Engine struct {
	deps   Dependencies
	logger *slog.Logger
	gate   *gate.Pool
}

const (
	searchPreReply      = "正在检索相关文档，请稍候。"
	imageOnlyUserPrompt = "请读取图片中的文字、界面和错误信息，将其视为用户本次输入，并直接处理图片中提出的问题。"
	emptyMentionPrompt  = "请简单介绍一下你自己。"
)

// NewEngine validates dependencies and starts the message gate.
func NewEngine(ctx context.Context, deps Dependencies, logger *slog.Logger) (*Engine, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if deps.Sender == nil {
		return nil, fmt.Errorf("bot sender is required")
	}
	engine := &Engine{deps: deps, logger: logger}
	engine.gate = gate.New(ctx, engine.gatePolicy, engine.onGateFlush, gate.Options{
		DedupTTL:      deps.Policy.Gate.DedupTTL,
		DedupMax:      deps.Policy.Gate.DedupMax,
		MaxConcurrent: deps.Policy.Gate.MaxConcurrent,
		IdleBucketTTL: time.Minute,
	})
	return engine, nil
}

// HandleInbound processes one normalized inbound message and returns a verdict
// describing whether it was handled, buffered, or rejected.
func (e *Engine) HandleInbound(ctx context.Context, msg messaging.InboundMessage) (platform.HandleResult, error) {
	e.logger.DebugContext(ctx, "bot inbound message", "conversation_id", msg.ConversationID, "message_id", msg.MessageID, "user_id", msg.UserID, "mentioned", msg.Mentioned, "text_len", len([]rune(msg.Text)))
	if err := validateInbound(msg); err != nil {
		return platform.HandleResult{Accepted: false, Verdict: "invalid"}, err
	}
	verdict := VerdictPass
	if e.deps.Policy.Gate.Enabled {
		verdict = Verdict(e.gate.Absorb(gate.Input{
			ConversationID:   msg.ConversationID,
			MsgID:            msg.MessageID,
			UserID:           msg.UserID,
			UserName:         msg.UserName,
			Text:             msg.Text,
			ReplyToMessageID: msg.ReplyToMessageID,
			PromptSuffix:     msg.PromptSuffix,
			Administrator:    msg.Administrator,
			Addressed:        msg.Addressed(),
			HasMedia:         msg.HasMedia(),
			At:               msg.At,
		}))
		if verdict != VerdictPass {
			return platform.HandleResult{Accepted: verdict == VerdictAbsorbed, Verdict: verdict.String()}, nil
		}
	}
	reply, err := e.replyText(ctx, msg)
	if err != nil {
		e.logger.DebugContext(ctx, "bot reply generation failed", "conversation_id", msg.ConversationID, "message_id", msg.MessageID, "err", err)
		return platform.HandleResult{Accepted: false, Verdict: "llm_failed"}, err
	}
	e.logger.DebugContext(ctx, "bot reply generated", "conversation_id", msg.ConversationID, "message_id", msg.MessageID, "reply_len", len([]rune(reply)))
	if err := e.reply(ctx, msg, reply); err != nil {
		e.logger.DebugContext(ctx, "bot reply delivery failed", "conversation_id", msg.ConversationID, "message_id", msg.MessageID, "err", err)
		return platform.HandleResult{Accepted: false, Verdict: "send_failed"}, err
	}
	return platform.HandleResult{Accepted: true, Verdict: verdict.String()}, nil
}

// Shutdown stops pending gate work and closes external tool executors.
func (e *Engine) Shutdown(grace time.Duration) {
	e.gate.Close(grace)
	if closer, ok := e.deps.Tools.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			e.logger.Warn("close tool executor failed", "err", err)
		}
	}
}

func (e *Engine) GateStats() gate.Stats {
	return e.gate.Stats()
}

func (e *Engine) gatePolicy(conversationID string) gate.Policy {
	policy := e.deps.Policy.Gate
	if groupPolicy, ok := e.deps.Policy.GroupGates[conversationID]; ok {
		policy.AutoReplyUnmentioned = groupPolicy.AutoReplyUnmentioned
	}
	return gate.Policy{
		Silence:              policy.Silence,
		MaxCount:             policy.MaxCount,
		MaxWindow:            policy.MaxWindow,
		MaxBuffered:          policy.MaxBuffered,
		MaxTextLen:           policy.MaxTextLen,
		AutoReplyUnmentioned: policy.AutoReplyUnmentioned,
	}
}

func (e *Engine) onGateFlush(ctx context.Context, batch gate.Batch) {
	transcript := batch.Transcript(e.deps.Policy.Gate.MaxTextLen)
	trigger := batch.Trigger("")
	if trigger.MsgID == "" && len(batch.Msgs) > 0 {
		trigger = batch.Msgs[len(batch.Msgs)-1]
	}
	in := messaging.InboundMessage{ConversationID: batch.ConversationID, UserID: trigger.UserID, MessageID: trigger.MsgID, Text: transcript, PromptSuffix: trigger.PromptSuffix}
	reply, err := e.replyText(ctx, in)
	if err != nil {
		e.logger.ErrorContext(ctx, "generate proactive gate reply", "err", err, "conversation_id", batch.ConversationID)
		return
	}
	if err := e.reply(ctx, in, reply); err != nil {
		e.logger.ErrorContext(ctx, "send proactive gate reply", "err", err, "conversation_id", batch.ConversationID)
	}
}

func (e *Engine) reply(ctx context.Context, in messaging.InboundMessage, text string) error {
	message := messaging.OutboundMessage{
		ConversationID:   in.ConversationID,
		ReplyToMessageID: in.MessageID,
		Text:             text,
	}
	return e.deps.Sender.Send(ctx, message)
}

func (e *Engine) replyText(ctx context.Context, in messaging.InboundMessage) (string, error) {
	text := strings.TrimSpace(in.Text)
	if text == "" && in.Mentioned && len(in.Media) == 0 {
		text = emptyMentionPrompt
		in.Text = text
	}
	instructions := e.instructions(in.PromptSuffix)
	if query, explicit := explicitSearchQuery(text); explicit {
		return e.explicitSearch(ctx, query, instructions)
	}
	if e.deps.LLM == nil {
		name := strings.TrimSpace(e.deps.Policy.Name)
		if name == "" {
			name = "ForgeBot"
		}
		return name + " received: " + strings.TrimSpace(text), nil
	}
	e.logger.DebugContext(ctx, "llm completion started", "model", e.deps.Policy.Model, "text_len", len([]rune(text)), "media_count", len(in.Media), "tools", len(e.toolDefinitions()))
	messages := []llm.Message{userMessage(in)}
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
	budget := e.deps.Policy.Search.MaxQueriesPerTurn
	if budget <= 0 {
		budget = 1
	}
	preReplySent := false
	for index, call := range response.Message.ToolCalls {
		result := tooling.Result{Code: "budget_exhausted"}
		if index < budget && e.deps.Tools != nil {
			if call.Name == "search_docs" && !e.deps.Policy.Search.Enabled {
				result = tooling.Result{Code: "tool_not_allowed"}
				payload, marshalErr := json.Marshal(result)
				if marshalErr != nil {
					payload = []byte(`{"ok":false,"code":"tool_result_failed"}`)
				}
				messages = append(messages, llm.ToolResultMessage(call.ID, call.Name, string(payload)))
				continue
			}
			if !preReplySent && call.Name == "search_docs" {
				preReplySent = true
				if err := e.reply(ctx, in, searchPreReply); err != nil {
					e.logger.WarnContext(ctx, "send search pre-reply failed", "conversation_id", in.ConversationID, "message_id", in.MessageID, "err", err)
				} else {
					e.logger.DebugContext(ctx, "search pre-reply sent", "conversation_id", in.ConversationID, "message_id", in.MessageID)
				}
			}
			result = e.deps.Tools.Execute(ctx, tooling.Call{Name: call.Name, Arguments: call.Arguments})
		}
		payload, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			payload = []byte(`{"ok":false,"code":"tool_result_failed"}`)
		}
		messages = append(messages, llm.ToolResultMessage(call.ID, call.Name, string(payload)))
	}
	startedAt = time.Now()
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

func userMessage(in messaging.InboundMessage) llm.Message {
	parts := make([]llm.ContentPart, 0, len(in.Media)+1)
	text := strings.TrimSpace(in.Text)
	if text != "" {
		parts = append(parts, llm.ContentPart{Type: "text", Text: text})
	}
	validImages := 0
	for _, media := range in.Media {
		if media.Kind != messaging.MediaImage {
			continue
		}
		part := llm.ContentPart{Type: "image", MediaURL: strings.TrimSpace(media.URL), MimeType: strings.TrimSpace(media.MimeType)}
		if len(media.Data) > 0 {
			if part.MimeType == "" {
				part.MimeType = "image/jpeg"
			}
			part.Data = base64.StdEncoding.EncodeToString(media.Data)
		}
		if part.MediaURL != "" || part.Data != "" {
			parts = append(parts, part)
			validImages++
		}
	}
	if text == "" && validImages > 0 {
		parts = append(parts, llm.ContentPart{Type: "text", Text: imageOnlyUserPrompt})
	}
	if len(parts) == 0 {
		parts = append(parts, llm.ContentPart{Type: "text", Text: strings.TrimSpace(in.Text)})
	}
	return llm.Message{Role: llm.RoleUser, Content: parts}
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
		if definition.Name == "search_docs" && !e.deps.Policy.Search.Enabled {
			continue
		}
		result = append(result, llm.ToolDefinition{Name: definition.Name, Description: definition.Description, InputSchema: definition.InputSchema})
	}
	return result
}

func (e *Engine) explicitSearch(ctx context.Context, query, instructions string) (string, error) {
	if !e.deps.Policy.Search.Enabled || e.deps.Retriever == nil {
		return "文档检索尚未启用。", nil
	}
	if query == "" {
		return "用法：/search <关键词或问题>", nil
	}
	response, err := e.deps.Retriever.Search(ctx, searchcontract.Query{Text: query, Limit: e.deps.Policy.Search.DefaultResults})
	if err != nil {
		e.logger.WarnContext(ctx, "explicit document search failed", "err", err)
		return "文档检索失败，请稍后重试。", nil
	}
	contextText, selected := searchcontract.BuildContext(response, e.deps.Policy.Search.MaxContextChars)
	if contextText == "" {
		return "未在文档库中找到可靠结果。", nil
	}
	if e.deps.LLM == nil {
		return formatSearchResults(selected), nil
	}
	result, err := e.deps.LLM.Complete(ctx, llm.Request{
		Model: e.deps.Policy.Model, Instructions: instructions,
		Messages: []llm.Message{
			llm.TextMessage(llm.RoleUser, query),
			llm.TextMessage(llm.RoleUser, contextText+"\n\n请根据以上资料回答原问题，并引用 source 路径。"),
		},
	})
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

func explicitSearchQuery(text string) (string, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", false
	}
	command := strings.ToLower(fields[0])
	if at := strings.IndexByte(command, '@'); at >= 0 {
		command = command[:at]
	}
	if command != "/search" {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(text, fields[0])), true
}

func formatSearchResults(results []searchcontract.Result) string {
	var builder strings.Builder
	builder.WriteString("文档检索结果：")
	for index, result := range results {
		fmt.Fprintf(&builder, "\n%d. %s", index+1, result.Path)
		if len(result.Headings) > 0 {
			builder.WriteString(" - " + strings.Join(result.Headings, " > "))
		}
		if result.Snippet != "" {
			builder.WriteString("\n" + result.Snippet)
		}
	}
	return builder.String()
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

type Verdict gate.Verdict

const (
	VerdictPass      Verdict = Verdict(gate.VerdictPass)
	VerdictAbsorbed  Verdict = Verdict(gate.VerdictAbsorbed)
	VerdictDuplicate Verdict = Verdict(gate.VerdictDuplicate)
	VerdictIgnored   Verdict = Verdict(gate.VerdictIgnored)
)

func (v Verdict) String() string {
	return gate.Verdict(v).String()
}
