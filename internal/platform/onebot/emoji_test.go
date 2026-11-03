package onebot

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmojiDescriptionsPreserveOtherContent(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`[{"type":"text","data":{"text":"你好"}},{"type":"face","data":{"id":"unknown","summary":"微笑"}},{"type":"text","data":{"text":"继续"}}]`, "你好【表情：微笑】继续"},
		{`[{"type":"face","data":{"id":"317"}}]`, ""},
		{`"你好[CQ:mface,summary=开心&#44;谢谢,url=http://example.invalid]继续[CQ:at,qq=42]"`, "你好【表情：开心,谢谢】继续"},
		{`"&#91;CQ:face,id=1&#93;&amp;"`, "[CQ:face,id=1]&"},
		{`[{"type":"text","data":{"text":"literal [CQ:face,id=1]"}}]`, "literal [CQ:face,id=1]"},
	} {
		if got := extractText(json.RawMessage(tc.raw)); got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
	if got := emojiText(strings.Repeat("字", 200)); len([]rune(got)) > 140 {
		t.Fatal("emoji description is unbounded")
	}
}
