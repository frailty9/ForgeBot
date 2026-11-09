// Package welcome 将实际成员入群事件转换为确定性欢迎模板，无模型依赖。
package welcome

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
)

const (
	ID          = "native.welcome"
	maxSegments = 64
	maxLength   = 4096
	userIDToken = "{user_id}"
)

// Message 既可以写成字符串，也可以写成消息段数组。消息段的形状沿用 OneBot：
// {"type":"at","data":{"qq":"{user_id}"}}。本模块不检查段的类型和 data 的含义，原样交给
// 连接器；只在字符串值里替换 {user}、{user_id}、{conversation_id}。
type Message struct {
	Text     string
	Segments []messaging.Segment
}

func (m *Message) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		return json.Unmarshal(trimmed, &m.Text)
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	// UseNumber 让 QQ 号等大整数原样往返，不被转成浮点数。
	decoder.UseNumber()
	return decoder.Decode(&m.Segments)
}

func (m Message) MarshalJSON() ([]byte, error) {
	if len(m.Segments) > 0 {
		return json.Marshal(m.Segments)
	}
	return json.Marshal(m.Text)
}

type GroupOptions struct {
	Enabled bool    `json:"enabled"`
	Message Message `json:"message"`
}
type Options struct {
	Groups map[string]GroupOptions `json:"groups"`
}
type Factory struct{}

func (Factory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: ID, DefaultEnabled: false, Registrations: []modules.RegistrationKind{modules.RegistrationGroupEvents}}
}

// validTemplate 只允许已知的模板变量，其余花括号一律视为拼写错误。
func validTemplate(text string) bool {
	text = strings.NewReplacer("{user}", "", userIDToken, "", "{conversation_id}", "").Replace(text)
	return !strings.ContainsAny(text, "{}")
}

// validateSegments 只检查结构边界：数量有界、每段有 type。段的类型和 data 的含义不检查，
// 由连接器和平台判断。
func validateSegments(segments []messaging.Segment) error {
	if len(segments) > maxSegments {
		return fmt.Errorf("welcome message has too many segments")
	}
	for _, segment := range segments {
		if strings.TrimSpace(segment.Type) == "" {
			return fmt.Errorf("welcome message segment requires a type")
		}
	}
	return nil
}

func parse(raw json.RawMessage) (Options, error) {
	result := Options{Groups: map[string]GroupOptions{}}
	if len(raw) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&result); err != nil {
			return result, err
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return result, fmt.Errorf("invalid welcome options suffix")
		}
	}
	for id, group := range result.Groups {
		message := group.Message
		if strings.TrimSpace(id) == "" || len(message.Text) > maxLength || (message.Text != "" && len(message.Segments) > 0) {
			return result, fmt.Errorf("welcome group requires a bounded message")
		}
		if group.Enabled && strings.TrimSpace(message.Text) == "" && len(message.Segments) == 0 {
			return result, fmt.Errorf("welcome group requires a nonblank message")
		}
		if !validTemplate(message.Text) {
			return result, fmt.Errorf("unsupported welcome template variable")
		}
		if err := validateSegments(message.Segments); err != nil {
			return result, err
		}
	}
	return result, nil
}
func (Factory) ValidateOptions(raw json.RawMessage) error      { _, err := parse(raw); return err }
func (Factory) PublicOptions(raw json.RawMessage) (any, error) { return parse(raw) }

// ReferencedGroups 供宿主核对欢迎模板引用的群均已绑定。
func (Factory) ReferencedGroups(raw json.RawMessage) ([]string, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(options.Groups))
	for id := range options.Groups {
		ids = append(ids, id)
	}
	return ids, nil
}
func (Factory) Build(_ context.Context, raw json.RawMessage) (modules.Instance, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return &instance{options: options}, nil
}

type instance struct{ options Options }

func (*instance) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (*instance) Close(context.Context) error        { return nil }
func (i *instance) ExtendedRegistrations() modules.ExtendedRegistrations {
	return modules.ExtendedRegistrations{GroupEvents: []modules.GroupEventRegistration{{ID: "member_joined", Handle: func(_ context.Context, event group.Event) (*group.EventReply, error) {
		options := i.options.Groups[event.ConversationID]
		if !options.Enabled || event.Kind != group.MemberJoined || event.Self || event.Historical {
			return nil, nil
		}
		return render(options.Message, event), nil
	}}}}
}

// render 只做确定性替换，且只替换解码后的字符串值：昵称里的引号、括号或 CQ 字面量
// 都只是文本，不会改变消息段结构。Text 是完整的纯文本形式，供不支持消息段的连接器使用。
func render(message Message, event group.Event) *group.EventReply {
	name := event.UserName
	if name == "" || len(name) > 256 {
		name = event.UserID
	}
	replacer := strings.NewReplacer("{user}", name, userIDToken, event.UserID, "{conversation_id}", event.ConversationID)
	if len(message.Segments) == 0 {
		return &group.EventReply{Text: replacer.Replace(message.Text)}
	}
	var plain strings.Builder
	reply := &group.EventReply{}
	for _, segment := range message.Segments {
		data, _ := substitute(segment.Data, replacer).(map[string]any)
		reply.Segments = append(reply.Segments, messaging.Segment{Type: segment.Type, Data: data})
		plain.WriteString(plainForm(segment.Type, data, event, name))
	}
	reply.Text = plain.String()
	return reply
}

// substitute 深拷贝并替换所有字符串值，不修改共享的配置对象。
func substitute(value any, replacer *strings.Replacer) any {
	switch typed := value.(type) {
	case string:
		return replacer.Replace(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = substitute(item, replacer)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = substitute(item, replacer)
		}
		return out
	default:
		return value
	}
}

// plainForm 为不支持消息段的连接器生成纯文本：text 取其文本，at 写成“@名字”，
// 其余类型（图片、表情等）无法降级，省略。
func plainForm(kind string, data map[string]any, event group.Event, name string) string {
	switch kind {
	case "text":
		text, _ := data["text"].(string)
		return text
	case "at":
		target := fmt.Sprint(data["qq"])
		if target == event.UserID {
			return "@" + name
		}
		return "@" + target
	default:
		return ""
	}
}
