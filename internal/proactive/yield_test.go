package proactive

import (
	"testing"
	"time"
)

func TestYieldExpiryNeverReplaysOldReplyAndGroupsAreIsolated(t *testing.T) {
	s := NewYieldState()
	now := time.Now()
	s.now = func() time.Time { return now }
	s.Configure([]string{"a", "b"})
	old, _ := s.Snapshot("a")
	other, _ := s.Snapshot("b")
	s.Suspend("a", time.Minute)
	if s.CanSend("a", old) || !s.CanSend("b", other) {
		t.Fatal("yield failed or crossed groups")
	}
	now = now.Add(2 * time.Minute)
	current, paused := s.Snapshot("a")
	if paused || s.CanSend("a", old) || !s.CanSend("a", current) {
		t.Fatal("expired yield replayed old response")
	}
	s.Configure([]string{"a", "b"})
	if !s.CanSend("a", current) {
		t.Fatal("unrelated generation update changed yield revision")
	}
	s.Configure([]string{"b"})
	if s.CanSend("a", current) {
		t.Fatal("unbound group still allowed")
	}
	s.Configure([]string{"a", "b"})
	if s.CanSend("a", current) {
		t.Fatal("rebinding resurrected old revision")
	}
}

func TestAdministratorQuoteAndMentionStillPasses(t *testing.T) {
	p := New(nil, func(string) Policy { return Policy{Silence: time.Hour, AutoReplyUnmentioned: true} }, nil, Options{})
	defer p.Close(time.Second)
	p.Absorb(Input{ConversationID: "g", UserID: "u", MsgID: "q", Text: "question"})
	if got := p.Absorb(Input{ConversationID: "g", UserID: "admin", MsgID: "a", ReplyToMessageID: "q", Text: "new question", Administrator: true, Addressed: true}); got != VerdictPass {
		t.Fatal("admin mention swallowed", got)
	}
	if p.HasPending("g", "q") {
		t.Fatal("quoted pending question not dismissed")
	}
}
