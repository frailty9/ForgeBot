// Package joinreview 按群提供入群自动审批规则。模块启用且群在配置中即开启
// 自动审批；模块只提供规则，判断、去重、执行前复核与调用连接器都由宿主完成。
package joinreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
)

const ID = "native.join_review"

// maxGroups 限制单个模块配置的群数量，避免超大配置拖慢校验。
const maxGroups = 1000

// GroupOptions 是一个群的审批规则；字段含义见 docs/group-management.md。
type GroupOptions struct {
	Regex        string `json:"regex,omitempty"`
	ModelEnabled *bool  `json:"model_enabled,omitempty"`
	Rule         string `json:"rule,omitempty"`
	Combine      string `json:"combine,omitempty"`
	RejectReason string `json:"reject_reason,omitempty"`
}

type Options struct {
	Groups map[string]GroupOptions `json:"groups"`
}

func (o GroupOptions) policy() group.Policy {
	return group.Policy{ReviewRegex: o.Regex, ReviewModelEnabled: o.ModelEnabled, ReviewRule: o.Rule, ReviewCombine: o.Combine, RejectReason: o.RejectReason}
}

type Factory struct{}

func (Factory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: ID, DefaultEnabled: false, Registrations: []modules.RegistrationKind{modules.RegistrationJoinReview}}
}

func parse(raw json.RawMessage) (Options, error) {
	result := Options{Groups: map[string]GroupOptions{}}
	if len(raw) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&result); err != nil {
			return Options{}, err
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return Options{}, errors.New("invalid join review options suffix")
		}
	}
	if result.Groups == nil {
		result.Groups = map[string]GroupOptions{}
	}
	if len(result.Groups) > maxGroups {
		return Options{}, fmt.Errorf("join review supports at most %d groups", maxGroups)
	}
	for id, options := range result.Groups {
		if strings.TrimSpace(id) == "" {
			return Options{}, errors.New("join review group id is required")
		}
		if err := options.policy().Validate(); err != nil {
			return Options{}, fmt.Errorf("join review group %q: %w", id, err)
		}
	}
	return result, nil
}

func (Factory) ValidateOptions(raw json.RawMessage) error      { _, err := parse(raw); return err }
func (Factory) PublicOptions(raw json.RawMessage) (any, error) { return parse(raw) }

func (Factory) ReferencedGroups(raw json.RawMessage) ([]string, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(options.Groups))
	for id := range options.Groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (Factory) Build(_ context.Context, raw json.RawMessage) (modules.Instance, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	policies := make(map[string]group.Policy, len(options.Groups))
	for id, groupOptions := range options.Groups {
		policies[id] = groupOptions.policy()
	}
	return &instance{policies: policies}, nil
}

// instance 构建后只读，规则随运行代替换，不在热更新中原地修改。
type instance struct{ policies map[string]group.Policy }

func (*instance) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (*instance) Close(context.Context) error        { return nil }
func (i *instance) ExtendedRegistrations() modules.ExtendedRegistrations {
	return modules.ExtendedRegistrations{JoinReview: &modules.JoinReviewRegistration{Policy: func(conversationID string) (group.Policy, bool) {
		policy, ok := i.policies[conversationID]
		return policy, ok
	}}}
}
