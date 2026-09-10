package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/gate"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
)

type Engine struct {
	cfg      config.Config
	logger   *slog.Logger
	registry *platform.Registry
	gate     *gate.Pool
}

func NewEngine(ctx context.Context, cfg config.Config, logger *slog.Logger, registry *platform.Registry) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	engine := &Engine{cfg: cfg, logger: logger, registry: registry}
	engine.gate = gate.New(ctx, engine.gatePolicy, engine.onGateFlush, gate.Options{
		DedupTTL:      cfg.Gate.DedupTTL,
		DedupMax:      cfg.Gate.DedupMax,
		MaxConcurrent: cfg.Gate.MaxConcurrent,
		IdleBucketTTL: time.Minute,
	})
	return engine
}

func (e *Engine) HandleInbound(ctx context.Context, msg messaging.InboundMessage) (platform.HandleResult, error) {
	if err := validateInbound(msg); err != nil {
		return platform.HandleResult{Accepted: false, Verdict: "invalid"}, err
	}
	verdict := VerdictPass
	if e.cfg.Gate.Enabled && msg.Group {
		verdict = Verdict(e.gate.Absorb(gate.Input{
			Platform:  msg.Platform,
			ChatID:    msg.ChatID,
			MsgID:     msg.MessageID,
			UserID:    msg.UserID,
			UserName:  msg.UserName,
			Text:      msg.Text,
			Addressed: msg.Addressed(),
			HasMedia:  msg.HasMedia(),
			At:        msg.At,
		}))
		if verdict != VerdictPass {
			return platform.HandleResult{Accepted: verdict == VerdictAbsorbed, Verdict: verdict.String()}, nil
		}
	}
	if err := e.reply(ctx, msg, "ForgeBot received: "+strings.TrimSpace(msg.Text)); err != nil {
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
		Silence:        e.cfg.Gate.Silence,
		MaxCount:       e.cfg.Gate.MaxCount,
		MaxWindow:      e.cfg.Gate.MaxWindow,
		MaxBuffered:    e.cfg.Gate.MaxBuffered,
		MaxTextLen:     e.cfg.Gate.MaxTextLen,
		RequireMention: e.cfg.Gate.RequireMention,
	}
}

func (e *Engine) onGateFlush(ctx context.Context, batch gate.Batch) {
	transcript := batch.Transcript(e.cfg.Gate.MaxTextLen)
	trigger := batch.Trigger("")
	if trigger.MsgID == "" && len(batch.Msgs) > 0 {
		trigger = batch.Msgs[len(batch.Msgs)-1]
	}
	text := "ForgeBot grouped message batch:\n" + transcript
	msg := messaging.OutboundMessage{
		Platform:         batch.Platform,
		ChatID:           batch.ChatID,
		ReplyToMessageID: trigger.MsgID,
		Text:             text,
	}
	if err := e.registry.Send(ctx, msg); err != nil {
		e.logger.ErrorContext(ctx, "send gate batch reply", "err", err, "platform", batch.Platform, "chat_id", batch.ChatID)
	}
}

func (e *Engine) reply(ctx context.Context, in messaging.InboundMessage, text string) error {
	return e.registry.Send(ctx, messaging.OutboundMessage{
		Platform:         in.Platform,
		ChatID:           in.ChatID,
		UserID:           in.UserID,
		ReplyToMessageID: in.MessageID,
		Text:             text,
	})
}

func validateInbound(msg messaging.InboundMessage) error {
	if strings.TrimSpace(msg.Platform) == "" {
		return fmt.Errorf("platform is required")
	}
	if strings.TrimSpace(msg.ChatID) == "" {
		return fmt.Errorf("chat_id is required")
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
