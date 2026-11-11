package telegram

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
)

type eventRecorder struct {
	mu       sync.Mutex
	events   []group.Event
	messages []messaging.InboundMessage
}

func (r *eventRecorder) HandleInbound(_ context.Context, m messaging.InboundMessage) (platform.HandleResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, m)
	return platform.HandleResult{}, nil
}

func (r *eventRecorder) HandleGroupEvent(_ context.Context, e group.Event) (group.EventResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return group.EventResult{Accepted: true}, nil
}

func joinMessage(date int64, members ...user) *message {
	m := testMessage()
	m.Text, m.Date = "", date
	// from 是邀请人，可能是机器人；不能被当作新成员，也不能因此丢弃整条服务消息。
	m.From = user{ID: 500, IsBot: true, FirstName: "Inviter"}
	m.NewMembers = members
	return &m
}

func TestNewMembersBecomeEventsWithMemberIdentityAndServiceDate(t *testing.T) {
	a := testAdapter(t)
	r := &eventRecorder{}
	date := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC).Unix()
	m := joinMessage(date, user{ID: 7, FirstName: "New", LastName: "Member"}, user{ID: 8, IsBot: true}, user{ID: 99, IsBot: true}, user{ID: 9, FirstName: "Second"})
	if err := a.handle(context.Background(), r, update{ID: 30, Message: m}); err != nil {
		t.Fatal(err)
	}
	if len(r.messages) != 0 {
		t.Fatal("service message handled as chat input", r.messages)
	}
	if len(r.events) != 2 {
		t.Fatal("bots or self welcomed", r.events)
	}
	first := r.events[0]
	if first.Kind != group.MemberJoined || first.UserID != "7" || first.UserName != "New Member" || first.ConversationID != "support" ||
		first.ConnectorID != "telegram" || !first.At.Equal(time.Unix(date, 0)) || first.Self || first.Historical {
		t.Fatal(first)
	}
	if first.ID == r.events[1].ID {
		t.Fatal("members of one service message share an event id")
	}
	// 同一真人短期内重新入群是新的 update，事件 ID 不同，不会被宿主当作重复。
	again := joinMessage(date+60, user{ID: 7, FirstName: "New"})
	if err := a.handle(context.Background(), r, update{ID: 31, Message: again}); err != nil {
		t.Fatal(err)
	}
	if len(r.events) != 3 || r.events[2].ID == first.ID {
		t.Fatal("rejoin collapsed into earlier event", r.events)
	}
}

func TestJoinServiceMessageWithoutValidDateIsNotWelcomed(t *testing.T) {
	a := testAdapter(t)
	r := &eventRecorder{}
	if err := a.handle(context.Background(), r, update{ID: 1, Message: joinMessage(0, user{ID: 7})}); err == nil || len(r.events) != 0 {
		t.Fatal("invalid date replaced by current time", err, r.events)
	}
	unbound := joinMessage(time.Now().Unix(), user{ID: 7})
	unbound.Chat.ID = -999
	if err := a.handle(context.Background(), r, update{ID: 2, Message: unbound}); err != nil || len(r.events) != 0 {
		t.Fatal("unbound group welcomed", err, r.events)
	}
}

func TestRedeliveredJoinUpdateIsDeduplicatedByUpdateID(t *testing.T) {
	a := testAdapter(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &receiver{ctx: ctx, cancel: cancel, queue: make(chan update, 4)}
	item := update{ID: 77, Message: joinMessage(time.Now().Unix(), user{ID: 7})}
	for range 2 {
		if !a.tryAccept(r, item) {
			t.Fatal("join update rejected")
		}
	}
	if len(r.queue) != 1 {
		t.Fatal("redelivered join update queued twice", len(r.queue))
	}
}
