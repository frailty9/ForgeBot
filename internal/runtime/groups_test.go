package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/group"
	groupfile "github.com/soleapi/forgebot/internal/group/filestore"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/welcome"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
)

type groupPort struct {
	platform.Sender
	calls atomic.Int32
}

func TestWelcomeOnlyActualJoinDeduplicatesAndHonorsPause(t *testing.T) {
	snapshot := testSnapshot()
	on := true
	snapshot.Runtime.Modules = map[string]config.ModuleConfig{welcome.ID: {Enabled: &on, Options: json.RawMessage(`{"groups":{"local":{"enabled":true,"text":"欢迎 {user_id}"}}}`)}}
	registry := modules.NewRegistry()
	if err := registry.Register(welcome.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	store, err := groupfile.Open(filepath.Join(t.TempDir(), "groups.json"))
	if err != nil {
		t.Fatal(err)
	}
	sender := registeredSender(t)
	if err := sender.registry.BindConversation(platform.ConversationBinding{ConversationID: "local", ConnectorID: "echo", Target: "local"}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: sender.registry, GroupStore: store, Modules: registry})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	event := group.Event{ID: "joined", Kind: group.MemberJoined, ConversationID: "local", ConnectorID: "echo", UserID: "user", At: time.Now()}
	manager.builder.Yield.Suspend("local", time.Minute)
	for i := 0; i < 2; i++ {
		if _, err = manager.HandleGroupEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	if len(sender.sender.Sent()) != 1 || sender.sender.Sent()[0].Text != "欢迎 user" {
		t.Fatal("welcome not sent once during proactive yield", sender.sender.Sent())
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

func (*groupPort) Support(context.Context, string) (group.Support, error) {
	return group.Support{JoinReview: true, Announcement: true, Recall: true, Permission: "unknown"}, nil
}
func (p *groupPort) Review(context.Context, string, group.ReviewRequest) error {
	p.calls.Add(1)
	return nil
}
func (p *groupPort) Announce(context.Context, string, string) error { p.calls.Add(1); return nil }
func (p *groupPort) Recall(context.Context, string, string) error   { p.calls.Add(1); return nil }

func TestGroupOperationsBoundAuthorizedAndIdempotent(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Runtime.GroupManagement = map[string]group.Policy{"local": {ReviewEnabled: true, Announcements: true, Recall: true}}
	store, err := groupfile.Open(filepath.Join(t.TempDir(), "groups.json"))
	if err != nil {
		t.Fatal(err)
	}
	port := &groupPort{Sender: registeredSender(t).registry}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: port, GroupStore: store})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	event := group.Event{ID: "request", Kind: group.JoinRequested, ConversationID: "local", ConnectorID: "echo", UserID: "u", Reference: "opaque", At: time.Now()}
	if r, err := manager.HandleGroupEvent(context.Background(), event); err != nil || r.Verdict != "pending" {
		t.Fatal(r, err)
	}
	list, err := manager.ListGroupOperations(context.Background(), 10)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if _, err = manager.ReviewJoinRequest(context.Background(), list[0].ID, 1, true, ""); err != nil {
		t.Fatal(err)
	}
	request := group.ActionRequest{ID: "announce", ConversationID: "local", Text: "notice"}
	if _, err = manager.PerformGroupAction(context.Background(), "announcement", request); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.PerformGroupAction(context.Background(), "announcement", request); err != nil {
		t.Fatal(err)
	}
	if port.calls.Load() != 2 {
		t.Fatal("duplicate side effects", port.calls.Load())
	}
	manager.SetPaused(true)
	request.ID = "paused"
	if _, err = manager.PerformGroupAction(context.Background(), "announcement", request); err == nil || operation.Describe(err).Code != operation.Disabled {
		t.Fatal("paused action executed", err)
	}
	if port.calls.Load() != 2 {
		t.Fatal("paused runtime called connector")
	}
}
