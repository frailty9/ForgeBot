// Package proactivereply provides bounded, delayed participation in selected
// group conversations without changing core message admission rules.
package proactivereply

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/history"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/proactive"
)

const ID = "native.proactive_reply"

// 决策规则上限 4096 字节，预留固定说明后 scope 最多 3072 字节。
const maxScopeBytes = 3072

// 待判断片段上限；连同 JSON 封装仍低于布尔决策的 16384 字节 state 上限。
const maxDecisionRunes = 3000

type GroupOptions struct {
	Enabled bool `json:"enabled"`
}

type Options struct {
	// Scope 只供主动插话决策判断话题是否值得介入，不注入回答模型；
	// 回答的范围约束由部署者写在 bot.system_prompt 中。
	Scope         string
	YieldDuration time.Duration
	Silence       time.Duration
	MaxWindow     time.Duration
	MaxBuffered   int
	MaxTextLen    int
	MaxConcurrent int
	// ContextMessages 是决策时附带的近期群聊条数（取自核心容器），0 表示不附带。
	// 建议不超过 message_history.max_messages 的 60%。
	ContextMessages int
	Groups          map[string]GroupOptions
}

type Factory struct{}

func (Factory) Descriptor() modules.Descriptor {
	return modules.Descriptor{
		ID: ID, DefaultEnabled: false,
		Hooks:    []hooks.Stage{hooks.BeforeReply},
		Provides: []capability.ID{proactive.ControlKey.ID()},
		Requires: []capability.ID{history.ReaderKey.ID()},
		Registrations: []modules.RegistrationKind{
			modules.RegistrationParticipants, modules.RegistrationStatus,
		},
	}
}

func defaults() Options {
	return Options{
		YieldDuration: 5 * time.Minute,
		Silence:       4 * time.Second, MaxWindow: 25 * time.Second,
		MaxBuffered: 50, MaxTextLen: 500, MaxConcurrent: 4, ContextMessages: 10,
		Groups: map[string]GroupOptions{},
	}
}

func parse(raw json.RawMessage) (Options, error) {
	result := defaults()
	if len(raw) == 0 {
		return result, nil
	}
	var value struct {
		Scope           string                  `json:"scope"`
		YieldDuration   string                  `json:"yield_duration"`
		Silence         string                  `json:"silence"`
		MaxWindow       string                  `json:"max_window"`
		MaxBuffered     *int                    `json:"max_buffered"`
		MaxTextLen      *int                    `json:"max_text_len"`
		MaxConcurrent   *int                    `json:"max_concurrent"`
		ContextMessages *int                    `json:"context_messages"`
		Groups          map[string]GroupOptions `json:"groups"`
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
	result.Scope = strings.TrimSpace(value.Scope)
	if len(result.Scope) > maxScopeBytes {
		return Options{}, errors.New("scope must not exceed 3072 bytes")
	}
	var err error
	if value.YieldDuration != "" {
		result.YieldDuration, err = time.ParseDuration(value.YieldDuration)
		if err != nil {
			return Options{}, err
		}
	}
	if result.YieldDuration <= 0 || result.YieldDuration > time.Hour*24 {
		return Options{}, errors.New("yield_duration must be positive and at most 24h")
	}
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
	if value.ContextMessages != nil {
		result.ContextMessages = *value.ContextMessages
	}
	if value.Groups != nil {
		result.Groups = value.Groups
	}
	if result.Silence < 0 || result.MaxWindow < 0 || result.MaxBuffered < 0 || result.MaxTextLen < 0 || result.MaxConcurrent <= 0 {
		return Options{}, errors.New("proactive reply limits are invalid")
	}
	if result.ContextMessages < 0 || result.ContextMessages > 50 {
		return Options{}, errors.New("context_messages must be 0..50")
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

// ReferencedGroups 供宿主核对主动插话配置的群均已绑定。
func (Factory) ReferencedGroups(raw json.RawMessage) ([]string, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(options.Groups))
	for id := range options.Groups {
		ids = append(ids, id)
	}
	return ids, nil
}

func (Factory) PublicOptions(raw json.RawMessage) (any, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"scope":          options.Scope,
		"yield_duration": options.YieldDuration.String(),
		"silence":        options.Silence.String(), "max_window": options.MaxWindow.String(),
		"max_buffered": options.MaxBuffered, "max_text_len": options.MaxTextLen,
		"max_concurrent": options.MaxConcurrent, "context_messages": options.ContextMessages, "groups": options.Groups,
	}, nil
}

func (Factory) Build(ctx context.Context, raw json.RawMessage) (modules.Instance, error) {
	return (Factory{}).BuildWithContext(ctx, modules.BuildContext{}, raw)
}

func (Factory) BuildWithContext(ctx context.Context, build modules.BuildContext, raw json.RawMessage) (modules.Instance, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	state, _ := capability.Resolve(build.Capabilities, proactive.YieldKey)
	if state == nil {
		state = proactive.NewYieldState()
	}
	decider, _ := capability.Resolve(build.Capabilities, decision.ServiceKey)
	recent, ok := capability.Resolve(build.Capabilities, history.ReaderKey)
	if !ok || recent == nil {
		return nil, errors.New("proactive reply requires the message history reader")
	}
	item := &instance{options: options, yield: state, decisions: decider, history: recent, now: time.Now}
	item.pool = proactive.New(ctx, item.policy, item.flush, proactive.Options{
		ManualStart:   true,
		MaxConcurrent: options.MaxConcurrent, IdleBucketTTL: time.Minute,
	})
	return item, nil
}

type instance struct {
	yield     *proactive.YieldState
	decisions decision.Decider
	options   Options
	pool      *proactive.Pool
	mu        sync.RWMutex
	responder modules.DeferredResponder

	history history.Reader
	now     func() time.Time
}

// answeredRecently 查核心容器：Bot 是否在 yield_duration 内回复过这条消息。管理员事后引用它
// （例如纠正 Bot），说明人已经接手这个话题，与引用待答问题一样让位。
func (i *instance) answeredRecently(conversationID, messageID string) bool {
	if messageID == "" {
		return false
	}
	now := i.now()
	for _, record := range i.history.Recent(conversationID, 0) {
		if record.Role == history.RoleBot && record.ReplyTo == messageID && now.Sub(record.At) < i.options.YieldDuration {
			return true
		}
	}
	return false
}

func (i *instance) Registrations() hooks.Registrations {
	return hooks.Registrations{BeforeReply: []hooks.Registration[hooks.ReplyInput, hooks.ReplyResult]{{ID: "yield_before_send", Handle: func(_ context.Context, info hooks.Info, _ hooks.ReplyInput) (hooks.ReplyResult, error) {
		return hooks.ReplyResult{Suppress: info.Trigger == hooks.TriggerProactive && !i.yield.CanSend(info.ConversationID, info.ProactiveRevision)}, nil
	}}}}
}

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

func (i *instance) handle(ctx context.Context, message messaging.InboundMessage, responder modules.DeferredResponder) (modules.ParticipantResult, error) {
	i.mu.Lock()
	i.responder = responder
	i.mu.Unlock()
	if message.TrustedResponder() && i.options.Groups[message.ConversationID].Enabled {
		intervened := message.ReplyToMessageID != "" && (i.pool.HasPending(message.ConversationID, message.ReplyToMessageID) || i.answeredRecently(message.ConversationID, message.ReplyToMessageID))
		pending := i.pool.PendingTranscript(message.ConversationID)
		if !intervened && !message.Addressed() && pending != "" && strings.TrimSpace(message.Text) != "" && i.decisions != nil {
			text := []rune(message.Text)
			if len(text) > 1000 {
				text = text[:1000]
			}
			state, _ := json.Marshal(map[string]any{"pending": pending, "administrator_reply": string(text)})
			answer, err := decision.DecideBool(ctx, i.decisions, "可信管理员是否正在对待答问题给出实质回答或明确接管；普通闲聊、只问新问题不算接管。", state)
			intervened = err == nil && answer
		}
		if intervened {
			i.yield.Suspend(message.ConversationID, i.options.YieldDuration)
			i.pool.DropConversation(message.ConversationID)
		}
	}
	revision, paused := i.yield.Snapshot(message.ConversationID)
	if paused && !message.Addressed() {
		return modules.ParticipantResult{Consume: true, Verdict: "proactive_yielded"}, nil
	}
	verdict := i.pool.Absorb(proactive.Input{
		Revision:       revision,
		ConversationID: message.ConversationID, MsgID: message.MessageID,
		UserID: message.UserID, UserName: message.UserName, Text: message.Text,
		ReplyToMessageID: message.ReplyToMessageID, PromptSuffix: message.PromptSuffix,
		Administrator: message.TrustedResponder(), Addressed: message.Addressed(),
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
	if !i.yield.CanSend(batch.ConversationID, trigger.Revision) {
		return
	}
	text := batch.Transcript(i.options.MaxTextLen)
	if !i.worthReplying(ctx, text, i.recentContext(batch)) {
		return
	}
	responder(ctx, messaging.InboundMessage{
		ProactiveRevision: trigger.Revision,
		ConversationID:    batch.ConversationID, UserID: trigger.UserID,
		UserName: trigger.UserName, MessageID: trigger.MsgID,
		Text: text, PromptSuffix: trigger.PromptSuffix,
	})
}

// maxRecentRunes 限制近期上下文的总长度；与待判断片段合计仍低于布尔决策的 state 上限。
const maxRecentRunes = 1500

// recentContext 从核心容器取这批消息之前的近期群聊，排除这批消息本身；超长时保留最新的几条。
func (i *instance) recentContext(batch proactive.Batch) string {
	if i.options.ContextMessages <= 0 {
		return ""
	}
	inBatch := make(map[string]bool, len(batch.Msgs))
	for _, m := range batch.Msgs {
		inBatch[m.MsgID] = true
	}
	var lines []string
	for _, r := range i.history.Recent(batch.ConversationID, i.options.ContextMessages+len(batch.Msgs)) {
		if r.MessageID == "" || !inBatch[r.MessageID] {
			lines = append(lines, recentLine(r))
		}
	}
	if len(lines) > i.options.ContextMessages {
		lines = lines[len(lines)-i.options.ContextMessages:]
	}
	budget, start := maxRecentRunes, len(lines)
	for start > 0 && budget-len([]rune(lines[start-1])) >= 0 {
		budget -= len([]rune(lines[start-1]))
		start--
	}
	return strings.Join(lines[start:], "")
}

func recentLine(r history.Record) string {
	speaker := r.UserName
	if speaker == "" {
		speaker = r.UserID
	}
	switch r.Role {
	case history.RoleBot:
		speaker = "[Bot]"
	case history.RoleAdmin:
		speaker = "[管理员] " + speaker
	}
	text := []rune(r.Text)
	if len(text) > 200 {
		text = append(text[:200], '…')
	}
	if r.HasMedia {
		text = append(text, []rune("[含媒体]")...)
	}
	return fmt.Sprintf("%s: %s\n", speaker, string(text))
}

// worthReplying 是主动插话的唯一一次决策：没有用户在等回复，误判就是打扰，
// 因此未配置决策服务、故障、无法判断或输入被截断时一律沉默。recent 是这批消息之前的
// 近期群聊（含管理员与 Bot 的发言），用来判断管理员是否已经接手，不增加模型调用。
func (i *instance) worthReplying(ctx context.Context, text, recent string) bool {
	if i.decisions == nil || strings.TrimSpace(text) == "" {
		return false
	}
	runes := []rune(text)
	if len(runes) > maxDecisionRunes {
		return false
	}
	rule := "data.messages 是群聊中未 @ 机器人的消息。判断是否值得机器人谨慎地主动回答：应是一个明确的、仍需要帮助的问题，且没有可信管理员已经接手。闲聊、感叹、已被解答或无需回复的内容都不符合。"
	if recent != "" {
		rule += "data.recent 是这些消息之前的近期群聊，[管理员] 是群主或群管理员，[Bot] 是机器人。如果管理员已经在回答或纠正同一话题（包括纠正机器人之前的回答），视为已经接手，不符合。"
	}
	if i.options.Scope != "" {
		rule += "此外，问题必须属于以下服务范围，范围外的专业问题不符合：" + i.options.Scope
	}
	data := map[string]string{"messages": text}
	if recent != "" {
		data["recent"] = recent
	}
	state, _ := json.Marshal(data)
	worth, err := decision.DecideBool(ctx, i.decisions, rule, state)
	return err == nil && worth
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
