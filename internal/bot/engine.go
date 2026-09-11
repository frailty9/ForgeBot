package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/gate"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
)

type Policy struct {
	Name         string
	SystemPrompt string
	Model        string
	Gate         GatePolicy
}

type GatePolicy struct {
	Enabled        bool
	RequireMention bool
	Silence        time.Duration
	MaxCount       int
	MaxWindow      time.Duration
	DedupTTL       time.Duration
	DedupMax       int
	MaxBuffered    int
	MaxTextLen     int
	MaxConcurrent  int
}

type Dependencies struct {
	Sender platform.Sender
	LLM    llm.Client
	Policy Policy
}

type Engine struct {
	deps   Dependencies
	logger *slog.Logger
	gate   *gate.Pool
}

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

func (e *Engine) HandleInbound(ctx context.Context, msg messaging.InboundMessage) (platform.HandleResult, error) {
	if err := validateInbound(msg); err != nil {
		return platform.HandleResult{Accepted: false, Verdict: "invalid"}, err
	}
	verdict := VerdictPass
	if e.deps.Policy.Gate.Enabled {
		verdict = Verdict(e.gate.Absorb(gate.Input{
			ConversationID: msg.ConversationID,
			MsgID:          msg.MessageID,
			UserID:         msg.UserID,
			UserName:       msg.UserName,
			Text:           msg.Text,
			Addressed:      msg.Addressed(),
			HasMedia:       msg.HasMedia(),
			At:             msg.At,
		}))
		if verdict != VerdictPass {
			return platform.HandleResult{Accepted: verdict == VerdictAbsorbed, Verdict: verdict.String()}, nil
		}
	}
	reply, err := e.replyText(ctx, msg.Text)
	if err != nil {
		return platform.HandleResult{Accepted: false, Verdict: "llm_failed"}, err
	}
	if err := e.reply(ctx, msg, reply); err != nil {
		return platform.HandleResult{Accepted: false, Verdict: "send_failed"}, err
	}
	return platform.HandleResult{Accepted: true, Verdict: verdict.String()}, nil
}

func (e *Engine) Shutdown(grace time.Duration) {
	e.gate.Close(grace)
}

func (e *Engine) GateStats() gate.Stats {
	return e.gate.Stats()
}

func (e *Engine) gatePolicy(platform string) gate.Policy {
	return gate.Policy{
		Silence:        e.deps.Policy.Gate.Silence,
		MaxCount:       e.deps.Policy.Gate.MaxCount,
		MaxWindow:      e.deps.Policy.Gate.MaxWindow,
		MaxBuffered:    e.deps.Policy.Gate.MaxBuffered,
		MaxTextLen:     e.deps.Policy.Gate.MaxTextLen,
		RequireMention: e.deps.Policy.Gate.RequireMention,
	}
}

func (e *Engine) onGateFlush(ctx context.Context, batch gate.Batch) {
	transcript := batch.Transcript(e.deps.Policy.Gate.MaxTextLen)
	trigger := batch.Trigger("")
	if trigger.MsgID == "" && len(batch.Msgs) > 0 {
		trigger = batch.Msgs[len(batch.Msgs)-1]
	}
	text := "ForgeBot grouped message batch:\n" + transcript
	msg := messaging.OutboundMessage{
		ConversationID:   batch.ConversationID,
		ReplyToMessageID: trigger.MsgID,
		Text:             text,
	}
	if err := e.deps.Sender.Send(ctx, msg); err != nil {
		e.logger.ErrorContext(ctx, "send gate batch reply", "err", err, "conversation_id", batch.ConversationID)
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

func (e *Engine) replyText(ctx context.Context, text string) (string, error) {
	if e.deps.LLM == nil {
		name := strings.TrimSpace(e.deps.Policy.Name)
		if name == "" {
			name = "ForgeBot"
		}
		return name + " received: " + strings.TrimSpace(text), nil
	}
	response, err := e.deps.LLM.Complete(ctx, llm.Request{
		Model:        e.deps.Policy.Model,
		Instructions: e.deps.Policy.SystemPrompt,
		Messages:     []llm.Message{llm.TextMessage(llm.RoleUser, strings.TrimSpace(text))},
	})
	if err != nil {
		return "", err
	}
	return response.Text, nil
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
	if strings.TrimSpace(msg.Text) == "" && len(msg.Media) == 0 {
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
