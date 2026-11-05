package filestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/operation"
)

func fixture(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "groups.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestTimeoutIsUnknownAndNeverAutomaticallyReplayed(t *testing.T) {
	store, path := fixture(t)
	s := group.New(store, nil, nil)
	calls := 0
	send := func(context.Context, string) error {
		calls++
		return operation.Wrap("remote", operation.Timeout, operation.EffectUnknown, context.DeadlineExceeded)
	}
	event := group.Event{ID: "e1", Kind: group.MemberJoined, ConversationID: "g", ConnectorID: "qq", UserID: "u", At: time.Now()}
	result, err := s.DeliverEvent(context.Background(), event, "native.welcome", group.EventReply{Text: "welcome"}, send)
	if !errors.Is(err, context.DeadlineExceeded) || result.Verdict != "send_failed" {
		t.Fatal(result, err)
	}
	if result, err = s.DeliverEvent(context.Background(), event, "native.welcome", group.EventReply{Text: "welcome"}, send); err != nil || result.Verdict != "duplicate" || calls != 1 {
		t.Fatal("unknown delivery replayed", result, err)
	}
	loaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	records, err := loaded.List(context.Background(), 10)
	if err != nil || len(records) != 1 || records[0].Status != group.Uncertain {
		t.Fatal(records, err)
	}
}

func TestExecutingStateRecoversUnknownAndRevisionIsolated(t *testing.T) {
	store, path := fixture(t)
	ctx := context.Background()
	r, _, err := store.Create(ctx, group.Record{ID: "action", Kind: "event_reply", ConversationID: "g", Status: group.Executing, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path, path+".replace-backup"); err != nil {
		t.Fatal(err)
	}
	loaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err = loaded.Get(ctx, r.ID)
	if err != nil || r.Status != group.Uncertain {
		t.Fatal(r, err)
	}
	r.Failure.Code = operation.Invalid
	current, _ := loaded.Get(ctx, r.ID)
	if current.Failure.Code != operation.Unknown {
		t.Fatal("failure alias")
	}
	if _, err = loaded.Update(ctx, 99, current); err == nil || operation.Describe(err).Code != operation.Conflict {
		t.Fatal(err)
	}
}
