package filestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/operation"
)

type actions struct {
	mu    sync.Mutex
	calls int
	fail  error
}

func (a *actions) Support(context.Context, string) (group.Support, error) {
	return group.Support{JoinReview: true, Announcement: true, Recall: true, Permission: "unknown"}, nil
}
func (a *actions) Review(context.Context, string, group.ReviewRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return a.fail
}
func (a *actions) Announce(context.Context, string, string) error {
	return a.Review(context.Background(), "", group.ReviewRequest{})
}
func (a *actions) Recall(context.Context, string, string) error {
	return a.Review(context.Background(), "", group.ReviewRequest{})
}

type decider struct {
	answer string
	calls  int
}

func (d *decider) Decide(context.Context, decision.Request) (decision.Result, error) {
	d.calls++
	return decision.Result{Answer: decision.Answer{"recommendation": d.answer}}, nil
}
func policy(d decision.Decider) group.Policies {
	return func(string) (group.Policy, decision.Decider, bool) {
		return group.Policy{ReviewEnabled: true, Announcements: true, Recall: true, AutoReview: d != nil, ReviewRule: "rule"}, d, true
	}
}
func fixture(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "groups.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestJoinReviewDuplicateCASAndOpaqueReference(t *testing.T) {
	store, _ := fixture(t)
	port := &actions{}
	s := group.New(store, port, policy(nil))
	ctx := context.Background()
	event := group.Event{ID: "event", Kind: group.JoinRequested, ConversationID: "g", ConnectorID: "one", UserID: "u", Reference: "opaque", At: time.Now()}
	if r, err := s.HandleEvent(ctx, event); err != nil || r.Verdict != "pending" {
		t.Fatal(r, err)
	}
	if r, err := s.HandleEvent(ctx, event); err != nil || r.Verdict != "duplicate" {
		t.Fatal(r, err)
	}
	public, err := s.List(ctx, 10)
	if err != nil || len(public) != 1 || public[0].Reference != "" {
		t.Fatal(public, err)
	}
	id := public[0].ID
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.Review(ctx, id, 1, true, "") }()
	}
	wg.Wait()
	if port.calls != 1 {
		t.Fatalf("duplicate review calls=%d", port.calls)
	}
	record, err := store.Get(ctx, id)
	if err != nil || record.Status != group.Done || record.Revision != 3 {
		t.Fatal(record, err)
	}
}

func TestTimeoutIsUnknownAndNeverAutomaticallyReplayed(t *testing.T) {
	store, path := fixture(t)
	port := &actions{fail: operation.Wrap("remote", operation.Timeout, operation.EffectUnknown, context.DeadlineExceeded)}
	s := group.New(store, port, policy(nil))
	request := group.ActionRequest{ID: "idempotent", ConversationID: "g", Text: "notice"}
	r, err := s.Perform(context.Background(), "announcement", request)
	if !errors.Is(err, context.DeadlineExceeded) || r.Status != group.Uncertain {
		t.Fatal(r, err)
	}
	if _, err = s.Perform(context.Background(), "announcement", request); err == nil || port.calls != 1 {
		t.Fatal("unknown action replayed", err)
	}
	loaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if r, err = loaded.Get(context.Background(), r.ID); err != nil || r.Status != group.Uncertain {
		t.Fatal(r, err)
	}
	request.Text = "different"
	if _, err = s.Perform(context.Background(), "announcement", request); err == nil || operation.Describe(err).Code != operation.Conflict {
		t.Fatal("reused key accepted", err)
	}
}

func TestExecutingStateRecoversUnknownAndRevisionIsolated(t *testing.T) {
	store, path := fixture(t)
	ctx := context.Background()
	r, _, err := store.Create(ctx, group.Record{ID: "action", Kind: "recall", ConversationID: "g", Status: group.Executing, CreatedAt: time.Now()})
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

func TestAutomaticReviewConsumesSharedDecisionAndUnknownWaitsHuman(t *testing.T) {
	for _, answer := range []string{"approve", decision.Unknown} {
		store, _ := fixture(t)
		port := &actions{}
		d := &decider{answer: answer}
		s := group.New(store, port, policy(d))
		r, err := s.HandleEvent(context.Background(), group.Event{ID: "event", Kind: group.JoinRequested, ConversationID: "g", ConnectorID: "one", UserID: "u", Reference: "opaque", Comment: "application", At: time.Now()})
		if err != nil || d.calls != 1 {
			t.Fatal(r, err)
		}
		want := 0
		if answer == "approve" {
			want = 1
		}
		if port.calls != want {
			t.Fatal("unknown approval performed", port.calls)
		}
	}
}
