package buffer

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/soleapi/forgebot/internal/messaging"
)

var (
	ErrInvalidConfig   = errors.New("reflection buffer configuration is invalid")
	ErrSessionNotFound = errors.New("reflection answer session was not found")
)

type Config struct {
	ConversationIDs            []string
	MessageTTL                 time.Duration
	QuietWindow                time.Duration
	MaxSessionDuration         time.Duration
	MaxMessagesPerConversation int
	MaxMessagesTotal           int
	MaxMessageChars            int
}

type AnswerSession struct {
	Key       string
	Question  messaging.InboundMessage
	Answers   []messaging.InboundMessage
	StartedAt time.Time
	LastAt    time.Time
}

type Stats struct {
	Questions int
	Sessions  int
	Messages  int
}

type question struct {
	message messaging.InboundMessage
	seenAt  time.Time
}

type session struct {
	AnswerSession
	adminID string
	closed  bool
}

type Buffer struct {
	mu            sync.Mutex
	conversations map[string]struct{}
	messageTTL    time.Duration
	quietWindow   time.Duration
	maxDuration   time.Duration
	maxPerGroup   int
	maxTotal      int
	maxChars      int
	questions     map[string]question
	questionOrder []string
	sessions      map[string]*session
}

func New(cfg Config) (*Buffer, error) {
	if cfg.MessageTTL <= 0 || cfg.QuietWindow <= 0 || cfg.MaxSessionDuration <= 0 ||
		cfg.MaxMessagesPerConversation <= 0 || cfg.MaxMessagesTotal <= 0 || cfg.MaxMessageChars <= 0 ||
		len(cfg.ConversationIDs) == 0 || cfg.QuietWindow > cfg.MaxSessionDuration {
		return nil, ErrInvalidConfig
	}
	conversations := make(map[string]struct{}, len(cfg.ConversationIDs))
	for _, id := range cfg.ConversationIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, ErrInvalidConfig
		}
		if _, exists := conversations[id]; exists {
			return nil, ErrInvalidConfig
		}
		conversations[id] = struct{}{}
	}
	return &Buffer{
		conversations: conversations, messageTTL: cfg.MessageTTL, quietWindow: cfg.QuietWindow,
		maxDuration: cfg.MaxSessionDuration, maxPerGroup: cfg.MaxMessagesPerConversation,
		maxTotal: cfg.MaxMessagesTotal, maxChars: cfg.MaxMessageChars,
		questions: make(map[string]question), sessions: make(map[string]*session),
	}, nil
}

// Observe uses the message timestamp when present and is intended for tests or
// callers that do not need an injected clock.
func (b *Buffer) Observe(message messaging.InboundMessage) bool {
	seenAt := message.At
	if seenAt.IsZero() {
		seenAt = time.Now()
	}
	return b.ObserveAt(seenAt, message)
}

// ObserveAt adds one message to the bounded observation window. It returns
// false when the message is not eligible for reflection.
func (b *Buffer) ObserveAt(now time.Time, message messaging.InboundMessage) bool {
	now = now.UTC()
	conversationID := strings.TrimSpace(message.ConversationID)
	if conversationID == "" || strings.TrimSpace(message.Text) == "" || message.Command ||
		utf8.RuneCountInString(message.Text) > b.maxChars {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(now)
	if _, allowed := b.conversations[conversationID]; !allowed {
		return false
	}
	message.ConversationID = conversationID
	message.Text = strings.TrimSpace(message.Text)
	if message.Administrator {
		return b.observeAdministratorLocked(now, message)
	}
	key := messageKey(conversationID, message.MessageID)
	if strings.TrimSpace(message.MessageID) == "" {
		return false
	}
	if _, exists := b.questions[key]; exists {
		return false
	}
	b.questions[key] = question{message: message, seenAt: now}
	b.questionOrder = append(b.questionOrder, key)
	b.enforceCapacityLocked()
	return true
}

func (b *Buffer) observeAdministratorLocked(now time.Time, message messaging.InboundMessage) bool {
	if strings.TrimSpace(message.UserID) == "" {
		return false
	}
	if replyID := strings.TrimSpace(message.ReplyToMessageID); replyID != "" {
		questionKey := messageKey(message.ConversationID, replyID)
		if candidate, exists := b.questions[questionKey]; exists && !candidate.message.Administrator {
			key := sessionKey(message.ConversationID, message.UserID, replyID)
			if existing, alreadyOpen := b.sessions[key]; alreadyOpen && !existing.closed {
				existing.Answers = append(existing.Answers, message)
				existing.LastAt = now
				return true
			}
			b.sessions[key] = &session{AnswerSession: AnswerSession{
				Key: key, Question: candidate.message, Answers: []messaging.InboundMessage{message},
				StartedAt: now, LastAt: now,
			}, adminID: message.UserID}
			delete(b.questions, questionKey)
			b.enforceCapacityLocked()
			return true
		}
	}
	var latest *session
	for _, candidate := range b.sessions {
		if candidate.closed || candidate.adminID != message.UserID || candidate.Question.ConversationID != message.ConversationID {
			continue
		}
		if now.Before(candidate.LastAt) || now.Sub(candidate.LastAt) > b.quietWindow || now.Sub(candidate.StartedAt) >= b.maxDuration {
			continue
		}
		if latest == nil || candidate.LastAt.After(latest.LastAt) {
			latest = candidate
		}
	}
	if latest == nil {
		return false
	}
	latest.Answers = append(latest.Answers, message)
	latest.LastAt = now
	b.enforceCapacityLocked()
	return true
}

// Ready returns closed answer sessions without removing them. Call Complete
// after publishing, or Retry after a transient reflection/publish failure.
func (b *Buffer) Ready(now time.Time) []AnswerSession {
	now = now.UTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(now)
	ready := make([]AnswerSession, 0)
	for _, candidate := range b.sessions {
		if !candidate.closed && (now.Sub(candidate.LastAt) >= b.quietWindow || now.Sub(candidate.StartedAt) >= b.maxDuration) {
			candidate.closed = true
		}
		if candidate.closed {
			ready = append(ready, cloneSession(candidate.AnswerSession))
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].StartedAt.Equal(ready[j].StartedAt) {
			return ready[i].Key < ready[j].Key
		}
		return ready[i].StartedAt.Before(ready[j].StartedAt)
	})
	return ready
}

func (b *Buffer) Complete(key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.sessions[key]; !exists {
		return ErrSessionNotFound
	}
	delete(b.sessions, key)
	return nil
}

func (b *Buffer) Retry(key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	candidate, exists := b.sessions[key]
	if !exists {
		return ErrSessionNotFound
	}
	candidate.closed = false
	return nil
}

func (b *Buffer) Stats(now time.Time) Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(now.UTC())
	return Stats{Questions: len(b.questions), Sessions: len(b.sessions), Messages: len(b.questions) + sessionMessages(b.sessions)}
}

func (b *Buffer) expireLocked(now time.Time) {
	for key, candidate := range b.questions {
		if now.Sub(candidate.seenAt) >= b.messageTTL {
			delete(b.questions, key)
		}
	}
	for key, candidate := range b.sessions {
		if now.Sub(candidate.LastAt) >= b.messageTTL || now.Sub(candidate.StartedAt) >= b.messageTTL+b.maxDuration {
			delete(b.sessions, key)
		}
	}
	b.compactQuestionOrderLocked()
}

func (b *Buffer) enforceCapacityLocked() {
	for b.totalMessagesLocked() > b.maxTotal || b.groupMessagesOverLimitLocked() != "" {
		conversationID := b.groupMessagesOverLimitLocked()
		var oldestQuestionKey string
		var oldestQuestion question
		for key, candidate := range b.questions {
			if conversationID != "" && candidate.message.ConversationID != conversationID {
				continue
			}
			if oldestQuestionKey == "" || candidate.seenAt.Before(oldestQuestion.seenAt) {
				oldestQuestionKey, oldestQuestion = key, candidate
			}
		}
		var oldestSessionKey string
		var oldestSession *session
		for key, candidate := range b.sessions {
			if conversationID != "" && candidate.Question.ConversationID != conversationID {
				continue
			}
			if oldestSession == nil || candidate.StartedAt.Before(oldestSession.StartedAt) {
				oldestSessionKey, oldestSession = key, candidate
			}
		}
		if oldestQuestionKey == "" && oldestSession == nil {
			break
		}
		if oldestSession == nil || (oldestQuestionKey != "" && oldestQuestion.seenAt.Before(oldestSession.StartedAt)) {
			delete(b.questions, oldestQuestionKey)
		} else {
			delete(b.sessions, oldestSessionKey)
		}
	}
	b.compactQuestionOrderLocked()
}

func (b *Buffer) totalMessagesLocked() int {
	total := len(b.questions)
	for _, candidate := range b.sessions {
		total += 1 + len(candidate.Answers)
	}
	return total
}

func (b *Buffer) groupMessagesOverLimitLocked() string {
	counts := make(map[string]int)
	for _, candidate := range b.questions {
		counts[candidate.message.ConversationID]++
	}
	for _, candidate := range b.sessions {
		counts[candidate.Question.ConversationID] += 1 + len(candidate.Answers)
	}
	for conversationID, count := range counts {
		if count > b.maxPerGroup {
			return conversationID
		}
	}
	return ""
}

func (b *Buffer) compactQuestionOrderLocked() {
	if len(b.questionOrder) < len(b.questions)*2+16 {
		return
	}
	order := b.questionOrder[:0]
	for _, key := range b.questionOrder {
		if _, exists := b.questions[key]; exists {
			order = append(order, key)
		}
	}
	b.questionOrder = order
}

func cloneSession(value AnswerSession) AnswerSession {
	value.Answers = append([]messaging.InboundMessage(nil), value.Answers...)
	return value
}

func sessionMessages(sessions map[string]*session) int {
	total := 0
	for _, candidate := range sessions {
		total += 1 + len(candidate.Answers)
	}
	return total
}

func messageKey(conversationID, messageID string) string { return conversationID + "\x00" + messageID }
func sessionKey(conversationID, userID, questionID string) string {
	return conversationID + "\x00" + userID + "\x00" + questionID
}
