package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/history"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
)

// historyWatcherFactory 是只订阅核心容器事件的测试模块；每一代实例带自己的代号。
type historyWatcherFactory struct {
	mu     sync.Mutex
	builds int
	seen   []string
}

func (*historyWatcherFactory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: "custom.history_watcher", DefaultEnabled: true, Registrations: []modules.RegistrationKind{modules.RegistrationHistory}}
}
func (*historyWatcherFactory) ValidateOptions(json.RawMessage) error      { return nil }
func (*historyWatcherFactory) PublicOptions(json.RawMessage) (any, error) { return nil, nil }
func (f *historyWatcherFactory) Build(context.Context, json.RawMessage) (modules.Instance, error) {
	f.mu.Lock()
	f.builds++
	generation := f.builds
	f.mu.Unlock()
	return &historyWatcher{factory: f, generation: generation}, nil
}
func (f *historyWatcherFactory) events() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

type historyWatcher struct {
	factory    *historyWatcherFactory
	generation int
}

func (*historyWatcher) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (*historyWatcher) Close(context.Context) error        { return nil }
func (w *historyWatcher) ExtendedRegistrations() modules.ExtendedRegistrations {
	return modules.ExtendedRegistrations{History: []history.Subscriber{{ID: "watch", Appended: func(r history.Record) {
		w.factory.mu.Lock()
		w.factory.seen = append(w.factory.seen, fmt.Sprintf("g%d:%s:%s", w.generation, r.Role, r.Text))
		w.factory.mu.Unlock()
	}}}}
}

// 收到的消息和 Bot 发送成功的回答都进入核心容器，回答记下它引用的消息；
// 事件只投递给当前一代模块，热更新后旧一代不再收到。
func TestHistoryRecordsInboundAndRepliesAndFollowsGenerations(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Runtime.Platforms.Groups[0].ID = "c"
	factory := &historyWatcherFactory{}
	client := moduleClient(func(context.Context, llm.Request) (llm.Response, error) { return llm.Response{Text: "answer"}, nil })
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry, Modules: moduleRegistry(t, factory),
		Secrets: configuredSecrets(t, "key"), ClientFactory: func(llm.ClientConfig) (llm.Client, error) { return client, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	ask := func(id, text string) {
		t.Helper()
		if _, err := manager.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "c", UserID: "u", MessageID: id, Text: text, Mentioned: true}); err != nil {
			t.Fatal(err)
		}
	}
	ask("q1", "first")
	records := manager.builder.History.Recent("c", 0)
	if len(records) != 2 || records[0].Role != history.RoleMember || records[0].MessageID != "q1" ||
		records[1].Role != history.RoleBot || records[1].ReplyTo != "q1" || records[1].Text != "answer" {
		t.Fatalf("recorded = %+v", records)
	}
	next := snapshot.Clone()
	next.Version++
	next.Runtime.Bot.Name = "renamed"
	if _, err = manager.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	ask("q2", "second")
	want := []string{"g1:member:first", "g1:bot:answer", "g2:member:second", "g2:bot:answer"}
	if got := factory.events(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if len(manager.builder.History.Recent("c", 0)) != 4 {
		t.Fatal("hot reload cleared the core message history")
	}
}

type flakySender struct{ fail bool }

func (s *flakySender) Send(context.Context, messaging.OutboundMessage) error {
	if s.fail {
		return errors.New("offline")
	}
	return nil
}

// 发送失败的消息不进入容器；容器没有开关，配置里写 enabled/disabled 会被拒绝。
func TestFailedSendIsNotRecordedAndHistoryHasNoSwitch(t *testing.T) {
	window := history.New(history.Policy{}, []string{"c"}, nil)
	sender := &flakySender{fail: true}
	wrapped := withHistory(sender, window)
	if err := wrapped.Send(context.Background(), messaging.OutboundMessage{ConversationID: "c", Text: "lost"}); err == nil {
		t.Fatal("send failure swallowed")
	}
	sender.fail = false
	if err := wrapped.Send(context.Background(), messaging.OutboundMessage{ConversationID: "c", ReplyToMessageID: "q", Text: "ok"}); err != nil {
		t.Fatal(err)
	}
	if got := window.Recent("c", 0); len(got) != 1 || got[0].Text != "ok" || got[0].ReplyTo != "q" {
		t.Fatalf("recorded = %+v", got)
	}
	for _, raw := range []string{`{"message_history":{"enabled":false}}`, `{"message_history":{"disabled":true}}`} {
		cfg := config.DefaultRuntime()
		if err := json.Unmarshal([]byte(raw), &cfg); err == nil {
			t.Fatal("message_history accepted a switch", raw)
		}
	}
}
