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
	"github.com/soleapi/forgebot/internal/modules"
)

const ID = "native.welcome"

type GroupOptions struct {
	Enabled bool   `json:"enabled"`
	Text    string `json:"text"`
}
type Options struct {
	Groups map[string]GroupOptions `json:"groups"`
}
type Factory struct{}

func (Factory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: ID, DefaultEnabled: false, Registrations: []modules.RegistrationKind{modules.RegistrationGroupEvents}}
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
		if strings.TrimSpace(id) == "" || len(group.Text) > 4096 || (group.Enabled && strings.TrimSpace(group.Text) == "") {
			return result, fmt.Errorf("welcome group requires bounded nonblank text")
		}
		text := strings.NewReplacer("{user}", "", "{user_id}", "", "{conversation_id}", "").Replace(group.Text)
		if strings.ContainsAny(text, "{}") {
			return result, fmt.Errorf("unsupported welcome template variable")
		}
	}
	return result, nil
}
func (Factory) ValidateOptions(raw json.RawMessage) error      { _, err := parse(raw); return err }
func (Factory) PublicOptions(raw json.RawMessage) (any, error) { return parse(raw) }
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
		name := event.UserName
		if name == "" {
			name = event.UserID
		}
		if len(name) > 256 {
			name = event.UserID
		}
		text := strings.NewReplacer("{user}", name, "{user_id}", event.UserID, "{conversation_id}", event.ConversationID).Replace(options.Text)
		return &group.EventReply{Text: text}, nil
	}}}}
}
