package onebot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/operation"
)

func TestGroupActionsUseOneBotAndVerifyRecallGroup(t *testing.T) {
	var paths []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if r.URL.Path == "/set_group_add_request" && (payload["flag"] != "opaque" || payload["sub_type"] != "add" || payload["approve"] != true) {
			t.Error("incorrect review payload", payload)
		}
		if r.URL.Path == "/_send_group_notice" && (payload["content"] != "notice" || payload["group_id"] != float64(8)) {
			t.Error("incorrect announcement", payload)
		}
		data := any(nil)
		if r.URL.Path == "/get_msg" {
			data = map[string]any{"message_type": "group", "group_id": 8}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "retcode": 0, "data": data})
	}))
	defer api.Close()
	s := &Sender{cfg: testConfig(api.URL), client: api.Client(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	reference, _ := json.Marshal(requestReference{Flag: "opaque", SubType: "add", Target: "8"})
	if err := s.ReviewGroupRequest(context.Background(), "8", group.ReviewRequest{Reference: string(reference), Approve: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishGroupAnnouncement(context.Background(), "8", "notice"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecallGroupMessage(context.Background(), "8", "1"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 || paths[2] != "/get_msg" || paths[3] != "/delete_msg" {
		t.Fatal(paths)
	}
	if err := s.RecallGroupMessage(context.Background(), "9", "1"); err == nil || operation.Describe(err).Code != operation.PolicyDenied || len(paths) != 5 {
		t.Fatal("cross-group recall allowed", err, paths)
	}
}

func TestGroupRequestAndActualMemberJoinAreDistinct(t *testing.T) {
	for _, raw := range []string{
		`{"post_type":"request","request_type":"group","sub_type":"add","group_id":8,"user_id":7,"self_id":99,"time":1700000000,"flag":"opaque","comment":"hello"}`,
		`{"post_type":"notice","notice_type":"group_increase","sub_type":"approve","group_id":8,"user_id":7,"self_id":99,"time":1700000001}`,
	} {
		var event rawEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		mapped, handled, err := event.groupEvent(testConfig(""))
		if err != nil || !handled || mapped.ID == "" || mapped.ConversationID != "support" {
			t.Fatal(mapped, handled, err)
		}
		if event.PostType == "request" && (mapped.Kind != group.JoinRequested || mapped.Reference == "") {
			t.Fatal(mapped)
		}
		if event.PostType == "notice" && (mapped.Kind != group.MemberJoined || mapped.Reference != "") {
			t.Fatal(mapped)
		}
	}
}
