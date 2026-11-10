package onebot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/soleapi/forgebot/internal/operation"
)

func TestRecallUsesDeleteMsgAndDoesNotStopAdapterOnFailure(t *testing.T) {
	for _, status := range []int{200, 403, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload struct {
					MessageID json.Number `json:"message_id"`
				}
				if r.URL.Path != "/delete_msg" || r.Method != http.MethodPost {
					t.Errorf("%s %s", r.Method, r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.MessageID.String() != "42" {
					t.Errorf("payload=%+v err=%v", payload, err)
				}
				w.WriteHeader(status)
				if status == 200 {
					_, _ = w.Write([]byte(`{"status":"ok","retcode":0}`))
				}
			}))
			defer api.Close()
			adapter, err := New(testConfig(api.URL), nil)
			if err != nil {
				t.Fatal(err)
			}
			err = adapter.sender.RecallMessage(context.Background(), "8", "42")
			if status == 200 && err != nil {
				t.Fatal(err)
			}
			if status == 403 && operation.Describe(err).Code != operation.PermissionDenied {
				t.Fatal(err)
			}
			if status == 404 && operation.Describe(err).Code != operation.Unsupported {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("recall retried")
			}
			select {
			case failure := <-adapter.Errors():
				t.Fatal("operation error leaked into lifecycle", failure)
			default:
			}
		})
	}
}
