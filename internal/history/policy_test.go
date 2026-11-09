package history

import (
	"encoding/json"
	"testing"
	"time"
)

// 容器没有开关：配置只接受三个上限参数，未知字段（包括 enabled/disabled）一律拒绝。
func TestPolicyJSONStrictAndDefaulted(t *testing.T) {
	var p Policy
	if err := json.Unmarshal([]byte(`{}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.MaxMessages != 50 || p.TTL != 30*time.Minute || p.MaxTextChars != 1000 {
		t.Fatalf("defaults = %+v", p)
	}
	if err := json.Unmarshal([]byte(`{"max_messages":20,"ttl":"10m","max_text_chars":500}`), &p); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(p)
	if string(encoded) != `{"max_messages":20,"max_text_chars":500,"ttl":"10m0s"}` {
		t.Fatal(string(encoded))
	}
	for _, raw := range []string{
		`{"enabled":false}`, `{"disabled":true}`, `{"max_messages":0.5}`, `{"max_messages":1001}`,
		`{"ttl":"0s"}`, `{"ttl":"25h"}`, `{"ttl":"soon"}`, `{"max_text_chars":9000}`, `{"ttl":null}`, `null`, `[]`,
	} {
		var bad Policy
		if err := json.Unmarshal([]byte(raw), &bad); err == nil {
			t.Fatal("invalid message_history accepted", raw)
		}
	}
}
