package knowledgereflection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/history"
	"github.com/soleapi/forgebot/internal/knowledge"
	"github.com/soleapi/forgebot/internal/reflection/pipeline"
	"github.com/soleapi/forgebot/internal/reflection/reflector"
	"github.com/soleapi/forgebot/internal/reflection/session"
)

type sessionReflector struct {
	seen   []session.AnswerSession
	handle func(context.Context) error
}

func (r *sessionReflector) Reflect(ctx context.Context, value session.AnswerSession) (reflector.Result, error) {
	r.seen = append(r.seen, value)
	if r.handle != nil {
		if err := r.handle(ctx); err != nil {
			return reflector.Result{}, err
		}
	}
	return reflector.Result{Accept: false}, nil
}

type unusedPublisher struct{}

func (unusedPublisher) Publish(knowledge.Entry) (knowledge.PublishResult, error) {
	panic("rejected candidates must not be published")
}

func sessionInstance(t *testing.T, change func(*Options)) (*instance, *history.Window, *sessionReflector, *time.Time) {
	t.Helper()
	options := defaults()
	options.ConversationIDs = []string{"support", "other"}
	options.QuietWindow = time.Minute
	options.MaxSessionDuration = 5 * time.Minute
	if change != nil {
		change(&options)
	}
	window := history.New(history.Policy{}, options.ConversationIDs, nil)
	r := &sessionReflector{}
	processor, err := pipeline.New(pipeline.Config{Reflector: r, Publisher: unusedPublisher{},
		MinConfidence: .9, Reload: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	i := newInstance(options, window, processor)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	i.now = func() time.Time { return now }
	window.SetSubscribers(i.ExtendedRegistrations().History)
	t.Cleanup(func() { _ = i.Close(context.Background()) })
	return i, window, r, &now
}

func member(id string) history.Record {
	return history.Record{ConversationID: "support", UserID: "member", MessageID: id, Role: history.RoleMember, Text: "Question " + id}
}

func admin(id, reply string) history.Record {
	return history.Record{ConversationID: "support", UserID: "admin", MessageID: id, ReplyTo: reply, Role: history.RoleAdmin, Text: "Answer " + id}
}

func TestSessionsRetainOnlyAnchoredAdministratorAnswers(t *testing.T) {
	i, w, _, _ := sessionInstance(t, nil)
	for n := 0; n < 100; n++ {
		w.Append(member(fmt.Sprint(n)))
	}
	w.Append(admin("unanchored", ""))
	ordinary := member("pretend")
	ordinary.ReplyTo = "99"
	w.Append(ordinary)
	if status := i.Status(); status.Pending.Messages != 0 || status.Pending.Sessions != 0 || status.Pending.Marks != 0 {
		t.Fatalf("ordinary messages retained: %+v", status.Pending)
	}
	w.Append(admin("a", "99"))
	foreign := admin("foreign", "99")
	foreign.ConversationID = "other"
	w.Append(foreign)
	if status := i.Status(); status.Pending.Messages != 2 || status.Pending.Sessions != 1 {
		t.Fatalf("unexpected sessions: %+v", status.Pending)
	}
}

func TestQuestionUsesHistoryBeforeQuotedFallback(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprint(fallback), func(t *testing.T) {
			i, w, r, now := sessionInstance(t, nil)
			if !fallback {
				w.Append(member("q"))
			}
			a := admin("a", "q")
			a.QuotedMessageID, a.QuotedText = "q", "Fallback question"
			w.Append(a)
			*now = now.Add(time.Minute)
			result, err := i.Scan(context.Background())
			if err != nil || result.Processed != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			want := "Question q"
			if fallback {
				want = "Fallback question"
			}
			if r.seen[0].Question.Text != want {
				t.Fatal(r.seen[0].Question)
			}
		})
	}
}

func TestMissingOrMismatchedQuoteAndKnownBotAreNotQuestions(t *testing.T) {
	i, w, _, _ := sessionInstance(t, nil)
	w.Append(admin("missing", "unknown"))
	quote := admin("mismatch", "unknown")
	quote.QuotedMessageID, quote.QuotedText = "different", "not the target"
	w.Append(quote)
	// 生产中 Bot 记录没有平台 ID（FromOutbound），Find 查不到；只能靠连接器标出的发送者排除。
	w.Append(history.Record{ConversationID: "support", Role: history.RoleBot, Text: "Bot reply"})
	quote = admin("to-bot", "bot-platform-id")
	quote.QuotedMessageID, quote.QuotedText, quote.QuotedFromBot = "bot-platform-id", "Bot reply", true
	w.Append(quote)
	// 截断标记由容器按 max_text_chars（默认 1000）计算，用真实超长正文触发。
	long := strings.Repeat("x", 1001)
	truncated := admin("truncated-quote", "gone")
	truncated.QuotedMessageID, truncated.QuotedText = "gone", long
	w.Append(truncated)
	w.Append(member("long"))
	cut := admin("cut-answer", "long")
	cut.Text = long
	w.Append(cut)
	command := member("command")
	command.Command = true
	w.Append(command)
	w.Append(admin("to-command", "command"))
	if pending := i.Status().Pending; pending.Sessions != 0 {
		t.Fatalf("invalid question opened session: %+v", pending)
	}
}

func TestCapturedSessionSurvivesHistoryEvictionAndAggregatesAnswers(t *testing.T) {
	i, w, r, now := sessionInstance(t, nil)
	w.Append(member("q"))
	w.Append(admin("a1", "q"))
	*now = now.Add(20 * time.Second)
	otherAdmin := admin("other-admin", "")
	otherAdmin.UserID = "another"
	w.Append(otherAdmin)
	w.Append(admin("a2", ""))
	w.Configure(history.Policy{MaxMessages: 1}, []string{"support", "other"})
	*now = now.Add(20 * time.Second)
	w.Append(admin("a3", "q"))
	w.Append(member("unrelated"))
	if _, exists := w.Find("support", "q"); exists {
		t.Fatal("question is still in history")
	}
	if result, err := i.Scan(context.Background()); err != nil || result.Processed != 0 {
		t.Fatalf("quiet window ignored: %+v %v", result, err)
	}
	*now = now.Add(time.Minute)
	result, err := i.Scan(context.Background())
	if err != nil || result.Processed != 1 || len(r.seen) != 1 || len(r.seen[0].Answers) != 3 {
		t.Fatalf("result=%+v err=%v sessions=%+v", result, err, r.seen)
	}
	w.Append(admin("reanchor", "q"))
	if i.Status().Pending.Sessions != 0 {
		t.Fatal("completed question was reopened")
	}
	if result, _ := i.Scan(context.Background()); result.Processed != 0 {
		t.Fatal("processed session repeated")
	}
}

func TestSessionCapsDropWholeOldestSessionAndMarksRemainBounded(t *testing.T) {
	i, w, _, now := sessionInstance(t, func(o *Options) {
		o.MaxMessagesPerConversation, o.MaxMessagesTotal = 3, 4
	})
	w.Append(member("q1"))
	w.Append(admin("a1", "q1"))
	*now = now.Add(time.Second)
	w.Append(member("q2"))
	w.Append(admin("a2", "q2"))
	if pending := i.Status().Pending; pending.Sessions != 1 || pending.Messages != 2 || pending.Dropped != 1 {
		t.Fatalf("per-group limit: %+v", pending)
	}
	for n := 0; n < 12; n++ {
		*now = now.Add(time.Second)
		q := member(fmt.Sprintf("q%d", n+3))
		a := admin(fmt.Sprintf("a%d", n+3), q.MessageID)
		if n%2 == 0 {
			q.ConversationID, a.ConversationID = "other", "other"
		}
		w.Append(q)
		w.Append(a)
		pending := i.Status().Pending
		if pending.Messages > 4 || pending.Marks > 4 {
			t.Fatalf("unbounded state: %+v", pending)
		}
	}
	*now = now.Add(25 * time.Hour)
	if pending := i.Status().Pending; pending.Sessions != 0 || pending.Marks != 0 || pending.Expired == 0 {
		t.Fatalf("expired state retained: %+v", pending)
	}
}

func TestLongestSessionTimeClosesContinuousAnswersAndBatchIsLimited(t *testing.T) {
	i, w, r, now := sessionInstance(t, func(o *Options) { o.MaxPairsPerScan = 1 })
	w.Append(member("q1"))
	w.Append(admin("a1", "q1"))
	w.Append(member("q2"))
	w.Append(admin("a2", "q2"))
	for n := 0; n < 9; n++ {
		*now = now.Add(30 * time.Second)
		w.Append(admin(fmt.Sprintf("continued%d", n), "q2"))
	}
	*now = now.Add(30 * time.Second)
	first, err := i.Scan(context.Background())
	if err != nil || first.Processed != 1 || first.Status.Worker.Ready != 2 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := i.Scan(context.Background())
	if err != nil || second.Processed != 1 || len(r.seen) != 2 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

func TestFailedSessionRetainsSnapshotForNextBatch(t *testing.T) {
	i, w, r, now := sessionInstance(t, nil)
	w.Append(member("q"))
	w.Append(admin("a", "q"))
	*now = now.Add(time.Minute)
	r.handle = func(context.Context) error { return errors.New("temporary") }
	first, err := i.Scan(context.Background())
	if err != nil || first.Retried != 1 || first.Status.Pending.Sessions != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	w.Configure(history.Policy{TTL: time.Nanosecond}, []string{"support", "other"})
	r.handle = nil
	second, err := i.Scan(context.Background())
	if err != nil || second.Processed != 1 || len(r.seen) != 2 || second.Status.Worker.Running {
		t.Fatalf("retry=%+v err=%v", second, err)
	}
}

func TestSingleBatchAndRetirementCancelInFlightWork(t *testing.T) {
	i, w, r, now := sessionInstance(t, nil)
	w.Append(member("q"))
	w.Append(admin("a", "q"))
	*now = now.Add(time.Minute)
	started := make(chan struct{})
	r.handle = func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }
	done := make(chan error, 1)
	go func() { _, err := i.Scan(context.Background()); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("batch did not start")
	}
	if result, err := i.Scan(context.Background()); err != nil || !result.Skipped {
		t.Fatalf("overlap=%+v err=%v", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := i.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	w.Append(admin("late", "q"))
	if i.Status().Pending.Sessions != 0 {
		t.Fatal("retired module accepted a message")
	}
	if _, err := i.Scan(context.Background()); err == nil {
		t.Fatal("retired module accepted a scan")
	}
}
