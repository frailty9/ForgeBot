package proactivereply

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/history"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/proactive"
)

type decisionFunc func(context.Context, decision.Request) (decision.Result, error)

func (f decisionFunc) Decide(ctx context.Context, r decision.Request) (decision.Result, error) {
	return f(ctx, r)
}

func testInstance(t *testing.T, d decision.Decider, silence string) (*instance, *proactive.YieldState) {
	t.Helper()
	state := proactive.NewYieldState()
	state.Configure([]string{"g"})
	values := capability.NewValues()
	_ = values.Provide(proactive.YieldKey.ID(), state)
	_ = values.Provide(history.ReaderKey.ID(), history.Reader(history.New(history.Policy{}, []string{"g"}, nil)))
	if d != nil {
		_ = values.Provide(decision.ServiceKey.ID(), d)
	}
	built, err := (Factory{}).BuildWithContext(context.Background(), modules.BuildContext{Capabilities: values.View()},
		json.RawMessage(`{"silence":"`+silence+`","max_window":"0s","groups":{"g":{"enabled":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	i := built.(*instance)
	t.Cleanup(func() { _ = i.Close(context.Background()) })
	return i, state
}

func TestUnrelatedQuotedContentNeverDirectlyYieldsOrDropsPending(t *testing.T) {
	for _, pending := range []bool{false, true} {
		for _, outcome := range []string{"no", "unknown", "error", "yes", "no-model"} {
			t.Run(outcome+map[bool]string{false: "/idle", true: "/pending"}[pending], func(t *testing.T) {
				calls := 0
				var d decision.Decider = decisionFunc(func(context.Context, decision.Request) (decision.Result, error) {
					calls++
					if outcome == "error" {
						return decision.Result{}, errors.New("offline")
					}
					return decision.Result{Answer: decision.Answer{"matches": outcome}}, nil
				})
				if outcome == "no-model" {
					d = nil
				}
				i, state := testInstance(t, d, "1h")
				if pending {
					_, _ = i.handle(context.Background(), messaging.InboundMessage{ConversationID: "g", UserID: "u", MessageID: "q", Text: "question"}, nil)
				}
				_, err := i.handle(context.Background(), messaging.InboundMessage{ConversationID: "g", UserID: "admin", MessageID: "a", PlatformRole: "admin",
					ReplyToMessageID: "yesterday", Quoted: &messaging.QuotedMessage{MessageID: "yesterday", Text: "old chat"}, Text: "hello"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				wantYield := pending && outcome == "yes"
				if _, paused := state.Snapshot("g"); paused != wantYield {
					t.Fatal("unrelated quote yielded", pending, outcome, paused)
				}
				if i.pool.HasPending("g", "q") != (pending && !wantYield) {
					t.Fatal("pending messages incorrectly discarded")
				}
				wantCalls := 0
				if pending && d != nil {
					wantCalls = 1
				}
				if calls != wantCalls {
					t.Fatal(calls, wantCalls)
				}
			})
		}
	}
}

func TestQuotedInflightQuestionYieldsAndInvalidatesGeneratedReply(t *testing.T) {
	// 主动回复需先经决策放行；引用命中在途问题时直接让位，不再调用决策。
	worth := decisionFunc(func(context.Context, decision.Request) (decision.Result, error) {
		return decision.Result{Answer: decision.Answer{"matches": "yes"}}, nil
	})
	i, state := testInstance(t, worth, "1ms")
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	revision, _ := state.Snapshot("g")
	_, err := i.handle(context.Background(), messaging.InboundMessage{ConversationID: "g", UserID: "u", MessageID: "q", Text: "question"},
		func(context.Context, messaging.InboundMessage) { close(entered); <-release })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("batch did not enter responder")
	}
	if !i.pool.HasPending("g", "q") {
		t.Fatal("inflight question missing")
	}
	_, err = i.handle(context.Background(), messaging.InboundMessage{ConversationID: "g", UserID: "a", MessageID: "a", PlatformRole: "admin", ReplyToMessageID: "q", Text: "answer"}, nil)
	if err != nil || state.CanSend("g", revision) {
		t.Fatal("inflight reply was not invalidated", err)
	}
}

// answerOnce 模拟生产中宿主写入核心容器的两条记录：成员的问题，以及 Bot 引用它发出的回答。
// 回答已发出，所以这条问题既不在缓冲也不在途。
func answerOnce(t *testing.T, i *instance, id string) {
	t.Helper()
	window := i.history.(*history.Window)
	window.Append(history.Record{ConversationID: "g", MessageID: id, UserID: "u", Role: history.RoleMember, Text: "question " + id})
	if _, ok := window.Append(history.Record{ConversationID: "g", Role: history.RoleBot, ReplyTo: id, Text: "answer"}); !ok {
		t.Fatal("bot answer not recorded")
	}
	if i.pool.HasPending("g", id) {
		t.Fatal("answered question still pending")
	}
}

// 管理员在 Bot 答完之后引用它回答过的问题（纠正或补充），说明人已接手，同样让位。
func TestAdminQuotingAnsweredQuestionYields(t *testing.T) {
	worth := decisionFunc(func(context.Context, decision.Request) (decision.Result, error) {
		return decision.Result{Answer: decision.Answer{"matches": "yes"}}, nil
	})
	i, state := testInstance(t, worth, "1ms")
	answerOnce(t, i, "q")
	if _, paused := state.Snapshot("g"); paused {
		t.Fatal("yielded before any administrator action")
	}
	_, err := i.handle(context.Background(), messaging.InboundMessage{ConversationID: "g", UserID: "a", MessageID: "a", PlatformRole: "admin", ReplyToMessageID: "q", Text: "不支持"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, paused := state.Snapshot("g"); !paused {
		t.Fatal("administrator quoting an answered question did not yield")
	}
}

// 只有 Bot 真正回答过的问题才算；引用无关消息、普通成员引用、超过让位时长后的引用都不触发。
func TestQuotingAnsweredQuestionYieldsOnlyForAdministratorsWithinWindow(t *testing.T) {
	worth := decisionFunc(func(context.Context, decision.Request) (decision.Result, error) {
		return decision.Result{Answer: decision.Answer{"matches": "yes"}}, nil
	})
	cases := []struct {
		name    string
		role    string
		replyTo string
		later   time.Duration
		want    bool
	}{
		{"unrelated message", "admin", "yesterday", 0, false},
		{"ordinary member", "member", "q", 0, false},
		{"after yield window", "admin", "q", 6 * time.Minute, false},
		{"within window", "owner", "q", 4 * time.Minute, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i, state := testInstance(t, worth, "1ms")
			answerOnce(t, i, "q")
			current := time.Now().Add(tc.later)
			i.now = func() time.Time { return current }
			_, err := i.handle(context.Background(), messaging.InboundMessage{ConversationID: "g", UserID: "a", MessageID: "a", PlatformRole: tc.role, ReplyToMessageID: tc.replyTo, Text: "hello"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, paused := state.Snapshot("g"); paused != tc.want {
				t.Fatalf("paused = %v, want %v", paused, tc.want)
			}
		})
	}
}

// 决策时附带核心容器里这批消息之前的近期群聊（含管理员与 Bot），不重复这批消息本身；
// 管理员已经接手时由同一次决策判为不值得回答，不增加模型调用。
func TestDecisionIncludesRecentContextExcludingBatch(t *testing.T) {
	var states []string
	worth := decisionFunc(func(_ context.Context, r decision.Request) (decision.Result, error) {
		states = append(states, string(r.State))
		return decision.Result{Answer: decision.Answer{"matches": "no"}}, nil
	})
	i, state := testInstance(t, worth, "1ms")
	window := i.history.(*history.Window)
	window.Append(history.Record{ConversationID: "g", MessageID: "q1", UserName: "小明", Role: history.RoleMember, Text: "能一键导入吗"})
	window.Append(history.Record{ConversationID: "g", Role: history.RoleBot, ReplyTo: "q1", Text: "可以导入"})
	window.Append(history.Record{ConversationID: "g", MessageID: "a1", UserName: "群主", Role: history.RoleAdmin, ReplyTo: "q1", Text: "不支持"})
	window.Append(history.Record{ConversationID: "g", MessageID: "q2", UserName: "小明", Role: history.RoleMember, Text: "那通用配置怎么写"})
	i.mu.Lock()
	i.responder = func(context.Context, messaging.InboundMessage) {
		t.Error("replied although the administrator took over")
	}
	i.mu.Unlock()
	revision, _ := state.Snapshot("g")
	i.flush(context.Background(), proactive.Batch{ConversationID: "g", Msgs: []proactive.Message{{Revision: revision, MsgID: "q2", UserName: "小明", Text: "那通用配置怎么写"}}})
	if len(states) != 1 {
		t.Fatalf("decision calls = %d", len(states))
	}
	var request struct {
		Rule string
		Data struct{ Messages, Recent string }
	}
	if err := json.Unmarshal([]byte(states[0]), &request); err != nil {
		t.Fatal(err)
	}
	data := request.Data
	if !strings.Contains(request.Rule, "data.recent") || !strings.Contains(request.Rule, "纠正") {
		t.Fatalf("rule does not explain the recent context: %s", request.Rule)
	}
	want := "小明: 能一键导入吗\n[Bot]: 可以导入\n[管理员] 群主: 不支持\n"
	if data.Recent != want {
		t.Fatalf("recent = %q", data.Recent)
	}
	if !strings.Contains(data.Messages, "那通用配置怎么写") || strings.Contains(data.Recent, "那通用配置怎么写") {
		t.Fatalf("batch messages leaked into recent context: %+v", data)
	}
}
