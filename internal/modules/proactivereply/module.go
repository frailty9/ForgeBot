// Package proactivereply provides bounded, delayed participation in selected
// group conversations without changing core message admission rules.
package proactivereply

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/proactive"
)

const ID = "native.proactive_reply"

type GroupOptions struct {
	Enabled bool `json:"enabled"`
}

type Options struct {
	Silence       time.Duration
	MaxWindow     time.Duration
	MaxBuffered   int
	MaxTextLen    int
	MaxConcurrent int
	Groups        map[string]GroupOptions
}

type Factory struct{}

func (Factory) Descriptor() modules.Descriptor {
	return modules.Descriptor{
		ID: ID, DefaultEnabled: false,
		Provides: []capability.ID{proactive.ControlKey.ID()},
		Registrations: []modules.RegistrationKind{
			modules.RegistrationParticipants, modules.RegistrationStatus,
		},
	}
}

func defaults() Options {
	return Options{
		Silence: 4 * time.Second, MaxWindow: 25 * time.Second,
		MaxBuffered: 50, MaxTextLen: 500, MaxConcurrent: 4,
		Groups: map[string]GroupOptions{},
	}
}

func parse(raw json.RawMessage) (Options, error) {
	result := defaults()
	if len(raw) == 0 {
		return result, nil
	}
	var value struct {
		Silence       string                  `json:"silence"`
		MaxWindow     string                  `json:"max_window"`
		MaxBuffered   *int                    `json:"max_buffered"`
		MaxTextLen    *int                    `json:"max_text_len"`
		MaxConcurrent *int                    `json:"max_concurrent"`
		Groups        map[string]GroupOptions `json:"groups"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Options{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Options{}, errors.New("invalid options suffix")
	}
	var err error
	if value.Silence != "" {
		result.Silence, err = time.ParseDuration(value.Silence)
		if err != nil {
			return Options{}, errors.New("silence must be a duration")
		}
	}
	if value.MaxWindow != "" {
		result.MaxWindow, err = time.ParseDuration(value.MaxWindow)
		if err != nil {
			return Options{}, errors.New("max_window must be a duration")
		}
	}
	if value.MaxBuffered != nil {
		result.MaxBuffered = *value.MaxBuffered
	}
	if value.MaxTextLen != nil {
		result.MaxTextLen = *value.MaxTextLen
	}
	if value.MaxConcurrent != nil {
		result.MaxConcurrent = *value.MaxConcurrent
	}
	if value.Groups != nil {
		result.Groups = value.Groups
	}
	if result.Silence < 0 || result.MaxWindow < 0 || result.MaxBuffered < 0 || result.MaxTextLen < 0 || result.MaxConcurrent <= 0 {
		return Options{}, errors.New("proactive reply limits are invalid")
	}
	for id := range result.Groups {
		if strings.TrimSpace(id) == "" {
			return Options{}, errors.New("group id is required")
		}
	}
	return result, nil
}

func (Factory) ValidateOptions(raw json.RawMessage) error {
	_, err := parse(raw)
	return err
}

func (Factory) PublicOptions(raw json.RawMessage) (any, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"silence": options.Silence.String(), "max_window": options.MaxWindow.String(),
		"max_buffered": options.MaxBuffered, "max_text_len": options.MaxTextLen,
		"max_concurrent": options.MaxConcurrent, "groups": options.Groups,
	}, nil
}

func (Factory) Build(ctx context.Context, raw json.RawMessage) (modules.Instance, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	item := &instance{options: options}
	item.pool = proactive.New(ctx, item.policy, item.flush, proactive.Options{
		ManualStart:   true,
		MaxConcurrent: options.MaxConcurrent, IdleBucketTTL: time.Minute,
	})
	return item, nil
}

type instance struct {
	options   Options
	pool      *proactive.Pool
	mu        sync.RWMutex
	responder modules.DeferredResponder
}

func (i *instance) Registrations() hooks.Registrations { return hooks.Registrations{} }

func (i *instance) ExtendedRegistrations() modules.ExtendedRegistrations {
	return modules.ExtendedRegistrations{
		Participants: []modules.ParticipantRegistration{{ID: "buffer_unaddressed", Handle: i.handle}},
		Capabilities: []modules.CapabilityRegistration{{ID: proactive.ControlKey.ID(), Value: proactive.Control(i)}},
		Status:       func() any { return i.Stats() },
	}
}

func (i *instance) policy(conversationID string) proactive.Policy {
	group := i.options.Groups[conversationID]
	return proactive.Policy{
		Silence: i.options.Silence, MaxWindow: i.options.MaxWindow,
		MaxBuffered: i.options.MaxBuffered, MaxTextLen: i.options.MaxTextLen,
		AutoReplyUnmentioned: group.Enabled,
	}
}

func (i *instance) handle(_ context.Context, message messaging.InboundMessage, responder modules.DeferredResponder) (modules.ParticipantResult, error) {
	i.mu.Lock()
	i.responder = responder
	i.mu.Unlock()
	verdict := i.pool.Absorb(proactive.Input{
		ConversationID: message.ConversationID, MsgID: message.MessageID,
		UserID: message.UserID, UserName: message.UserName, Text: message.Text,
		ReplyToMessageID: message.ReplyToMessageID, PromptSuffix: message.PromptSuffix,
		Administrator: message.Administrator, Addressed: message.Addressed(),
		HasMedia: message.HasMedia(), At: message.At,
	})
	if verdict == proactive.VerdictPass {
		return modules.ParticipantResult{}, nil
	}
	return modules.ParticipantResult{
		Consume: true, Accepted: verdict == proactive.VerdictAbsorbed, Verdict: verdict.String(),
	}, nil
}

func (i *instance) flush(ctx context.Context, batch proactive.Batch) {
	i.mu.RLock()
	responder := i.responder
	i.mu.RUnlock()
	if responder == nil {
		return
	}
	trigger := batch.Trigger("")
	if trigger.MsgID == "" && len(batch.Msgs) > 0 {
		trigger = batch.Msgs[len(batch.Msgs)-1]
	}
	responder(ctx, messaging.InboundMessage{
		ConversationID: batch.ConversationID, UserID: trigger.UserID,
		UserName: trigger.UserName, MessageID: trigger.MsgID,
		Text: batch.Transcript(i.options.MaxTextLen), PromptSuffix: trigger.PromptSuffix,
	})
}

func (i *instance) Stats() proactive.Stats { return i.pool.Stats() }
func (i *instance) Start(context.Context)  { i.pool.Start() }
func (i *instance) Retire()                { i.pool.DropPending() }
func (i *instance) Close(ctx context.Context) error {
	grace := time.Duration(0)
	if deadline, ok := ctx.Deadline(); ok {
		grace = time.Until(deadline)
	}
	i.pool.Close(grace)
	return nil
}
