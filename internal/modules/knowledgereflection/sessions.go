package knowledgereflection

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/history"
	"github.com/soleapi/forgebot/internal/messaging"
	reflectioncontract "github.com/soleapi/forgebot/internal/reflection"
	"github.com/soleapi/forgebot/internal/reflection/pipeline"
	"github.com/soleapi/forgebot/internal/reflection/session"
)

type answerState struct {
	session.AnswerSession
	administrator string
	closed        bool
	processing    bool
}

type processedMark struct {
	conversation string
	at           time.Time
}

// instance 只保存已经锚定的答题会话；普通成员消息由宿主 history 保存。
// 会话材料是快照，原记录滑出后仍可处理，不订阅或依赖移出事件。
type instance struct {
	options  Options
	reader   history.Reader
	pipeline *pipeline.Processor
	allowed  map[string]bool
	now      func() time.Time
	lifetime context.Context
	cancel   context.CancelFunc

	mu       sync.Mutex
	sessions map[string]*answerState
	marks    map[string]processedMark
	pending  reflectioncontract.PendingStats
	worker   reflectioncontract.WorkerStats
	active   bool
	retired  bool
	idle     chan struct{}
	scanMu   sync.Mutex
}

func newInstance(options Options, reader history.Reader, processor *pipeline.Processor) *instance {
	lifetime, cancel := context.WithCancel(context.Background())
	allowed := make(map[string]bool, len(options.ConversationIDs))
	for _, id := range options.ConversationIDs {
		allowed[strings.TrimSpace(id)] = true
	}
	idle := make(chan struct{})
	close(idle)
	return &instance{options: options, reader: reader, pipeline: processor, allowed: allowed,
		now: time.Now, lifetime: lifetime, cancel: cancel, sessions: map[string]*answerState{},
		marks: map[string]processedMark{}, idle: idle}
}

func (i *instance) observe(record history.Record) {
	if !i.allowed[record.ConversationID] || record.Role != history.RoleAdmin || record.Command ||
		record.UserID == "" || record.MessageID == "" || strings.TrimSpace(record.Text) == "" ||
		utf8.RuneCountInString(record.Text) > i.options.MaxMessageChars {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.retired {
		return
	}
	now := i.now().UTC()
	i.expireLocked(now)
	messageMark := "message:" + group.ID(record.ConversationID, record.MessageID)
	if _, seen := i.marks[messageMark]; seen {
		return
	}
	answer := inbound(record)
	var target *answerState
	if record.ReplyTo != "" {
		key := group.ID(record.ConversationID, record.UserID, record.ReplyTo)
		if _, completed := i.marks["session:"+key]; completed {
			return
		}
		target = i.sessions[key]
		if target == nil {
			question, ok := i.question(record)
			if !ok {
				return
			}
			target = &answerState{AnswerSession: session.AnswerSession{Key: key,
				Question: question, StartedAt: now, LastAt: now}, administrator: record.UserID}
			i.sessions[key] = target
		}
	} else {
		for _, candidate := range i.sessions {
			if candidate.Question.ConversationID != record.ConversationID || candidate.administrator != record.UserID ||
				candidate.closed || i.ready(candidate, now) {
				continue
			}
			if target == nil || candidate.LastAt.After(target.LastAt) ||
				(candidate.LastAt.Equal(target.LastAt) && candidate.Key < target.Key) {
				target = candidate
			}
		}
	}
	if target == nil || target.closed || (len(target.Answers) > 0 && i.ready(target, now)) {
		return
	}
	target.Answers = append(target.Answers, answer)
	target.LastAt = now
	i.markLocked(messageMark, record.ConversationID, now)
	i.enforceCapacityLocked(now)
}

// question 只使用同群已知记录或当前消息自带的引用原文，不额外查询平台。
func (i *instance) question(answer history.Record) (messaging.InboundMessage, bool) {
	var question messaging.InboundMessage
	if record, ok := i.reader.Find(answer.ConversationID, answer.ReplyTo); ok {
		if record.Role != history.RoleMember || record.Command {
			return question, false
		}
		question = inbound(record)
	} else if answer.QuotedMessageID == answer.ReplyTo {
		question = messaging.InboundMessage{ConversationID: answer.ConversationID,
			MessageID: answer.ReplyTo, Text: answer.QuotedText}
	}
	question.Text = strings.TrimSpace(question.Text)
	return question, question.Text != "" && utf8.RuneCountInString(question.Text) <= i.options.MaxMessageChars
}

func inbound(record history.Record) messaging.InboundMessage {
	role := ""
	if record.Role == history.RoleAdmin {
		role = "admin"
	}
	return messaging.InboundMessage{ConversationID: record.ConversationID, UserID: record.UserID,
		UserName: record.UserName, MessageID: record.MessageID, ReplyToMessageID: record.ReplyTo,
		Text: strings.TrimSpace(record.Text), PlatformRole: role, At: record.At}
}

func (i *instance) ready(candidate *answerState, now time.Time) bool {
	return candidate.closed || !now.Before(candidate.LastAt.Add(i.options.QuietWindow)) ||
		!now.Before(candidate.StartedAt.Add(i.options.MaxSessionDuration))
}

func (i *instance) expireLocked(now time.Time) {
	for key, candidate := range i.sessions {
		if !candidate.processing && (!now.Before(candidate.LastAt.Add(i.options.MessageTTL)) ||
			!now.Before(candidate.StartedAt.Add(i.options.MessageTTL+i.options.MaxSessionDuration))) {
			delete(i.sessions, key)
			i.pending.Expired++
		}
	}
	for key, mark := range i.marks {
		if !now.Before(mark.at.Add(i.options.MessageTTL)) {
			delete(i.marks, key)
		}
	}
}

// markLocked 的上限复用模块容量，完成后只保留标记，不保留正文。
func (i *instance) markLocked(key, conversation string, now time.Time) {
	i.marks[key] = processedMark{conversation: conversation, at: now}
	for {
		count := 0
		for _, mark := range i.marks {
			if mark.conversation == conversation {
				count++
			}
		}
		perGroup := count > i.options.MaxMessagesPerConversation
		if !perGroup && len(i.marks) <= i.options.MaxMessagesTotal {
			return
		}
		oldestKey := ""
		var oldest processedMark
		for candidateKey, mark := range i.marks {
			if perGroup && mark.conversation != conversation {
				continue
			}
			if oldestKey == "" || mark.at.Before(oldest.at) || (mark.at.Equal(oldest.at) && candidateKey < oldestKey) {
				oldestKey, oldest = candidateKey, mark
			}
		}
		delete(i.marks, oldestKey)
	}
}

func (i *instance) countsLocked() (int, map[string]int) {
	total := 0
	groups := map[string]int{}
	for _, candidate := range i.sessions {
		count := 1 + len(candidate.Answers)
		total += count
		groups[candidate.Question.ConversationID] += count
	}
	return total, groups
}

func (i *instance) enforceCapacityLocked(now time.Time) {
	for {
		total, counts := i.countsLocked()
		overGroup := ""
		for id, count := range counts {
			if count > i.options.MaxMessagesPerConversation && (overGroup == "" || id < overGroup) {
				overGroup = id
			}
		}
		if overGroup == "" && total <= i.options.MaxMessagesTotal {
			return
		}
		var oldest *answerState
		for _, candidate := range i.sessions {
			if candidate.processing || (overGroup != "" && candidate.Question.ConversationID != overGroup) {
				continue
			}
			if oldest == nil || candidate.StartedAt.Before(oldest.StartedAt) ||
				(candidate.StartedAt.Equal(oldest.StartedAt) && candidate.Key < oldest.Key) {
				oldest = candidate
			}
		}
		// 在途批次已占满该群容量时，新候选会作为最旧的非在途项被释放。
		if oldest == nil {
			return
		}
		delete(i.sessions, oldest.Key)
		i.markLocked("session:"+oldest.Key, oldest.Question.ConversationID, now)
		i.pending.Dropped++
	}
}

func (i *instance) batchLocked(now time.Time) []session.AnswerSession {
	i.expireLocked(now)
	var candidates []*answerState
	for _, candidate := range i.sessions {
		if i.ready(candidate, now) {
			candidates = append(candidates, candidate)
		}
	}
	sort.Slice(candidates, func(a, b int) bool {
		if candidates[a].StartedAt.Equal(candidates[b].StartedAt) {
			return candidates[a].Key < candidates[b].Key
		}
		return candidates[a].StartedAt.Before(candidates[b].StartedAt)
	})
	i.worker.Ready = len(candidates)
	if len(candidates) > i.options.MaxPairsPerScan {
		candidates = candidates[:i.options.MaxPairsPerScan]
	}
	batch := make([]session.AnswerSession, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.closed, candidate.processing = true, true
		copy := candidate.AnswerSession
		copy.Answers = append([]messaging.InboundMessage(nil), copy.Answers...)
		batch = append(batch, copy)
	}
	return batch
}
