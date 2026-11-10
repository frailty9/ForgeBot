// Package joinreview 提供入群自动审批规则。一个部署对应一个业务，规则在模块层
// 统一配置，groups 只决定哪些群开启；模块只提供规则，判断、去重、执行前复核
// 与调用连接器都由宿主完成。
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

// GroupOptions 只决定该群是否开启审批。一个部署对应一个业务，规则在模块层
// 统一配置；保留对象形式便于将来增加群级字段而不改格式。
type GroupOptions struct {
	Enabled bool `json:"enabled"`
}

// Options 的规则字段对所有开启的群生效；字段含义见 docs/group-management.md。
type Options struct {
	Regex          string                  `json:"regex,omitempty"`
	ModelEnabled   *bool                   `json:"model_enabled,omitempty"`
	Rule           string                  `json:"rule,omitempty"`
	Combine        string                  `json:"combine,omitempty"`
	RejectReason   string                  `json:"reject_reason,omitempty"`
	Groups         map[string]GroupOptions `json:"groups"`
	ExternalReview *group.ExternalReview   `json:"external_review,omitempty"`
}

func (o Options) policy() group.Policy {
	return group.Policy{ReviewRegex: o.Regex, ReviewModelEnabled: o.ModelEnabled, ReviewRule: o.Rule, ReviewCombine: o.Combine, RejectReason: o.RejectReason, ExternalReview: o.ExternalReview}
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
	for id := range result.Groups {
		if strings.TrimSpace(id) == "" {
			return Options{}, errors.New("join review group id is required")
		}
	}
	// 列了群或填了规则就校验规则；关闭的群同样校验，避免重新开启时才发现错误。
	if policy := result.policy(); len(result.Groups) > 0 || policy != (group.Policy{}) {
		if err := policy.Validate(); err != nil {
			return Options{}, fmt.Errorf("join review: %w", err)
		}
	}
	return result, nil
}

func (Factory) ValidateOptions(raw json.RawMessage) error { _, err := parse(raw); return err }
func (Factory) PublicOptions(raw json.RawMessage) (any, error) {
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	if options.ExternalReview != nil {
		external := *options.ExternalReview
		external.Headers = make(map[string]string, len(options.ExternalReview.Headers))
		for name := range options.ExternalReview.Headers {
			external.Headers[name] = "[configured]"
		}
		options.ExternalReview = &external
	}
	return options, nil
}

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
	enabled := make(map[string]bool, len(options.Groups))
	for id, groupOptions := range options.Groups {
		if groupOptions.Enabled {
			enabled[id] = true
		}
	}
	return &instance{policy: options.policy(), enabled: enabled}, nil
}

// instance 构建后只读，规则随运行代替换，不在热更新中原地修改。
// 所有开启的群共用同一份规则。
type instance struct {
	policy  group.Policy
	enabled map[string]bool
}

func (*instance) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (*instance) Close(context.Context) error        { return nil }
func (i *instance) ExtendedRegistrations() modules.ExtendedRegistrations {
	return modules.ExtendedRegistrations{JoinReview: &modules.JoinReviewRegistration{Policy: func(conversationID string) (group.Policy, bool) {
		if !i.enabled[conversationID] {
			return group.Policy{}, false
		}
		return i.policy, true
	}}}
}
