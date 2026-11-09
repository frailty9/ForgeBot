package runtime

import (
	"context"
	"encoding/json"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/joinreview"
	"github.com/soleapi/forgebot/internal/modules/welcome"
	"github.com/soleapi/forgebot/internal/platform"
)

type groupPort struct {
	platform.Sender
	calls atomic.Int32
}

func TestWelcomeSendsOnJoinAndIgnoresSelfHistoricalAndPause(t *testing.T) {
	snapshot := testSnapshot()
	on := true
	snapshot.Runtime.Modules = map[string]config.ModuleConfig{welcome.ID: {Enabled: &on, Options: json.RawMessage(`{"groups":{"local":{"enabled":true,"message":"欢迎 {user_id}"}}}`)}}
	registry := modules.NewRegistry()
	if err := registry.Register(welcome.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	sender := registeredSender(t)
	if err := sender.registry.BindConversation(platform.ConversationBinding{ConversationID: "local", ConnectorID: "echo", Target: "local"}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: sender.registry, Modules: registry})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	event := group.Event{ID: "joined", Kind: group.MemberJoined, ConversationID: "local", ConnectorID: "echo", UserID: "user", At: time.Now()}
	manager.builder.Yield.Suspend("local", time.Minute)
	if _, err = manager.HandleGroupEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(sender.sender.Sent()) != 1 || sender.sender.Sent()[0].Text != "欢迎 user" {
		t.Fatal("welcome not sent during proactive yield", sender.sender.Sent())
	}
	event.ID = "self"
	event.Self = true
	_, _ = manager.HandleGroupEvent(context.Background(), event)
	event.Self = false
	event.ID = "historical"
	event.Historical = true
	_, _ = manager.HandleGroupEvent(context.Background(), event)
	event.Historical = false
	event.ID = "paused"
	manager.SetPaused(true)
	_, _ = manager.HandleGroupEvent(context.Background(), event)
	if len(sender.sender.Sent()) != 1 {
		t.Fatal("unexpected self/historical/paused welcome")
	}
}

func TestWelcomeSegmentsReachSenderWithPlainTextForm(t *testing.T) {
	snapshot := testSnapshot()
	on := true
	options := `{"groups":{"local":{"enabled":true,"message":[{"type":"text","data":{"text":"欢迎 "}},{"type":"at","data":{"qq":"{user_id}"}}]}}}`
	snapshot.Runtime.Modules = map[string]config.ModuleConfig{welcome.ID: {Enabled: &on, Options: json.RawMessage(options)}}
	registry := modules.NewRegistry()
	if err := registry.Register(welcome.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	sender := registeredSender(t)
	if err := sender.registry.BindConversation(platform.ConversationBinding{ConversationID: "local", ConnectorID: "echo", Target: "local"}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: sender.registry, Modules: registry})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	event := group.Event{ID: "joined", Kind: group.MemberJoined, ConversationID: "local", ConnectorID: "echo", UserID: "42", UserName: "Alice", At: time.Now()}
	if _, err = manager.HandleGroupEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	sent := sender.sender.Sent()
	want := []messaging.Segment{{Type: "text", Data: map[string]any{"text": "欢迎 "}}, {Type: "at", Data: map[string]any{"qq": "42"}}}
	if len(sent) != 1 || sent[0].Text != "欢迎 @Alice" || !reflect.DeepEqual(sent[0].Segments, want) {
		t.Fatal("segments not delivered", sent)
	}
}

func (*groupPort) Support(context.Context, string) (group.Support, error) {
	return group.Support{JoinReview: true, Permission: "unknown"}, nil
}
func (p *groupPort) Review(context.Context, string, group.ReviewRequest) error {
	p.calls.Add(1)
	return nil
}

// joinReviewSnapshot 在 local 群上启用 native.join_review，返回快照与对应的模块注册表。
func joinReviewSnapshot(t *testing.T, regex string) (config.Snapshot, *modules.Registry) {
	t.Helper()
	snapshot := testSnapshot()
	on := true
	options, _ := json.Marshal(map[string]any{"regex": regex, "groups": map[string]any{"local": map[string]any{"enabled": true}}})
	snapshot.Runtime.Modules = map[string]config.ModuleConfig{joinreview.ID: {Enabled: &on, Options: options}}
	return snapshot, moduleRegistry(t, joinreview.Factory{})
}

func TestAutomaticReviewKeepsNoRecordsAndHonorsPause(t *testing.T) {
	snapshot, registry := joinReviewSnapshot(t, `\S`)
	port := &groupPort{Sender: registeredSender(t).registry}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: port, Modules: registry})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	event := group.Event{ID: "request", Kind: group.JoinRequested, ConversationID: "local", ConnectorID: "echo", UserID: "u", Reference: "opaque", Comment: "application", At: time.Now()}
	if r, err := manager.HandleGroupEvent(context.Background(), event); err != nil || r.Verdict != "approved" {
		t.Fatal(r, err)
	}
	if port.calls.Load() != 1 {
		t.Fatal("review not executed exactly once", port.calls.Load())
	}
	manager.SetPaused(true)
	event.ID = "paused"
	_, _ = manager.HandleGroupEvent(context.Background(), event)
	if port.calls.Load() != 1 {
		t.Fatal("paused runtime called connector")
	}
}
