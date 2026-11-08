package onebot

import (
	"encoding/json"
	"testing"
)

func TestPlatformRoleMarksTrustedResponder(t *testing.T) {
	var event rawEvent
	if err := json.Unmarshal([]byte(`{"post_type":"message","message_type":"group","message_id":1,"user_id":2,"group_id":8,"self_id":99,"sender":{"nickname":"owner","role":"owner"},"message":[{"type":"text","data":{"text":"answer"}}]}`), &event); err != nil {
		t.Fatal(err)
	}
	message, err := event.message(testConfig("").Groups)
	if err != nil {
		t.Fatal(err)
	}
	if !message.TrustedResponder() || message.PlatformRole != "owner" {
		t.Fatal("platform owner role not recognized as trusted responder", message)
	}
}
