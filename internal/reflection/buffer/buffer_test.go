package buffer

import (
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
)

func testBuffer(t *testing.T) *Buffer {
	t.Helper()
	b, err := New(Config{ConversationIDs: []string{"support"}, MessageTTL: 24 * time.Hour, QuietWindow: 3 * time.Minute, MaxSessionDuration: 30 * time.Minute, MaxMessagesPerConversation: 10, MaxMessagesTotal: 20, MaxMessageChars: 4000})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func msg(conversation, user, id, text string, admin bool) messaging.InboundMessage {
	return messaging.InboundMessage{ConversationID: conversation, UserID: user, MessageID: id, Text: text, Administrator: admin}
}

func TestBufferAggregatesMultiMessageAdministratorAnswer(t *testing.T) {
	b := testBuffer(t)
	base := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	if !b.ObserveAt(base, msg("support", "member", "q1", "How do I rotate the key?", false)) {
		t.Fatal("question rejected")
	}
	answer := msg("support", "admin", "a1", "Open the console.", true)
	answer.ReplyToMessageID = "q1"
	if !b.ObserveAt(base.Add(time.Minute), answer) {
		t.Fatal("answer anchor rejected")
	}
	if !b.ObserveAt(base.Add(2*time.Minute), msg("support", "admin", "a2", "Then choose API Keys.", true)) {
		t.Fatal("continuation rejected")
	}
	reanchor := msg("support", "admin", "a3", "Also revoke the old key.", true)
	reanchor.ReplyToMessageID = "q1"
	if !b.ObserveAt(base.Add(2*time.Minute+30*time.Second), reanchor) {
		t.Fatal("re-anchored continuation rejected")
	}
	if b.ObserveAt(base.Add(2*time.Minute+31*time.Second), reanchor) {
		t.Fatal("duplicate answer accepted")
	}
	got := b.Ready(base.Add(6 * time.Minute))
	if len(got) != 1 || len(got[0].Answers) != 3 || got[0].Question.MessageID != "q1" {
		t.Fatalf("ready = %+v", got)
	}
	if err := b.Complete(got[0].Key); err != nil {
		t.Fatal(err)
	}
}

func TestBufferRequiresAdministratorReplyAnchorAndScopesGroups(t *testing.T) {
	b := testBuffer(t)
	base := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	b.ObserveAt(base, msg("support", "member", "q1", "Question", false))
	if b.ObserveAt(base.Add(time.Minute), msg("support", "admin", "a1", "Unanchored", true)) {
		t.Fatal("unanchored answer accepted")
	}
	foreign := msg("other", "member", "q2", "Question", false)
	if b.ObserveAt(base, foreign) {
		t.Fatal("unconfigured conversation accepted")
	}
	answer := msg("support", "admin", "a2", "Answer", true)
	answer.ReplyToMessageID = "q1"
	if !b.ObserveAt(base.Add(2*time.Minute), answer) {
		t.Fatal("anchored answer rejected")
	}
	if b.ObserveAt(base.Add(3*time.Minute), msg("support", "admin2", "a3", "Other admin", true)) {
		t.Fatal("other administrator continued session")
	}
}

func TestBufferExpiresQuestionsAndEnforcesCapacity(t *testing.T) {
	b, err := New(Config{ConversationIDs: []string{"support"}, MessageTTL: time.Hour, QuietWindow: time.Minute, MaxSessionDuration: time.Hour, MaxMessagesPerConversation: 2, MaxMessagesTotal: 2, MaxMessageChars: 20})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		b.ObserveAt(base.Add(time.Duration(i)*time.Second), msg("support", "u", string(rune('1'+i)), "question", false))
	}
	if got := b.Stats(base.Add(3 * time.Second)); got.Questions != 2 {
		t.Fatalf("stats = %+v", got)
	}
	if got := b.Stats(base.Add(2 * time.Hour)); got.Questions != 0 {
		t.Fatalf("expired stats = %+v", got)
	}
}

func TestBufferEnforcesGlobalCapacityAcrossConversations(t *testing.T) {
	b, err := New(Config{ConversationIDs: []string{"one", "two"}, MessageTTL: time.Hour, QuietWindow: time.Minute, MaxSessionDuration: time.Hour, MaxMessagesPerConversation: 10, MaxMessagesTotal: 2, MaxMessageChars: 20})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	b.ObserveAt(base, msg("one", "u", "q1", "one", false))
	b.ObserveAt(base.Add(time.Second), msg("two", "u", "q2", "two", false))
	b.ObserveAt(base.Add(2*time.Second), msg("one", "u", "q3", "three", false))
	if got := b.Stats(base.Add(3 * time.Second)); got.Messages != 2 || got.Questions != 2 {
		t.Fatalf("global capacity stats = %+v", got)
	}
}

func TestBufferEnforcesCapacityAfterRepeatedAnchor(t *testing.T) {
	b, err := New(Config{ConversationIDs: []string{"support"}, MessageTTL: time.Hour, QuietWindow: time.Minute, MaxSessionDuration: time.Hour, MaxMessagesPerConversation: 2, MaxMessagesTotal: 2, MaxMessageChars: 20})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	b.ObserveAt(base, msg("support", "u", "q1", "question", false))
	answer := msg("support", "admin", "a1", "answer", true)
	answer.ReplyToMessageID = "q1"
	b.ObserveAt(base.Add(time.Second), answer)
	reanchor := msg("support", "admin", "a2", "more", true)
	reanchor.ReplyToMessageID = "q1"
	b.ObserveAt(base.Add(2*time.Second), reanchor)
	if got := b.Stats(base.Add(3 * time.Second)); got.Messages > 2 {
		t.Fatalf("capacity exceeded after repeated anchor: %+v", got)
	}
}
