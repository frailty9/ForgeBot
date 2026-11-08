package joinreview

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/soleapi/forgebot/internal/modules"
)

func registration(t *testing.T, raw string) *modules.JoinReviewRegistration {
	t.Helper()
	instance, err := (Factory{}).Build(context.Background(), json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	result := instance.(modules.ExtendedProvider).ExtendedRegistrations().JoinReview
	if result == nil {
		t.Fatal("join review registration missing")
	}
	return result
}

// 规则在模块层统一配置，所有开启的群共用；关闭或未列出的群不审批。
func TestJoinReviewSharesModulePolicyAcrossEnabledGroups(t *testing.T) {
	raw := `{"regex":"^.{4,}$","combine":"or","rule":"必须说明来意","model_enabled":true,"reject_reason":"请说明来意",
		"groups":{"qq-main":{"enabled":true},"qq-backup":{"enabled":true},"paused":{"enabled":false}}}`
	r := registration(t, raw)
	for _, id := range []string{"qq-main", "qq-backup"} {
		policy, ok := r.Policy(id)
		if !ok || policy.ReviewRegex != "^.{4,}$" || policy.Combine() != "or" || !policy.ModelEnabled() || policy.RejectReason != "请说明来意" {
			t.Fatalf("%s policy = %+v %v", id, policy, ok)
		}
	}
	for _, id := range []string{"paused", "unlisted"} {
		if _, ok := r.Policy(id); ok {
			t.Fatalf("%s must not be reviewed", id)
		}
	}
	groups, err := (Factory{}).ReferencedGroups(json.RawMessage(raw))
	if err != nil || len(groups) != 3 || groups[0] != "paused" || groups[1] != "qq-backup" || groups[2] != "qq-main" {
		t.Fatal("disabled groups must still be checked for binding", groups, err)
	}
}

// 未列群也未填规则是合法的空配置；列了群就必须有可用规则。
// 旧的按群规则写法、旧字段名与群值写成布尔都被拒绝。
func TestJoinReviewOptionsValidation(t *testing.T) {
	for _, valid := range []string{`{}`, `{"groups":{}}`, `{"regex":".","groups":{"g":{"enabled":false}}}`} {
		if err := (Factory{}).ValidateOptions(json.RawMessage(valid)); err != nil {
			t.Fatalf("valid options rejected: %s: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		`{"groups":{"g":{"enabled":true}}}`,
		`{"groups":{"g":{"regex":"."}}}`,
		`{"groups":{"g":true},"regex":"."}`,
		`{"review_regex":".","groups":{"g":{"enabled":true}}}`,
		`{"auto_review":true,"regex":"."}`,
		`{"regex":"(","groups":{"g":{"enabled":true}}}`,
		`{"regex":"^(?=a)","groups":{"g":{"enabled":true}}}`,
		`{"regex":".","combine":"xor","groups":{"g":{"enabled":true}}}`,
		`{"regex":".","groups":{" ":{"enabled":true}}}`,
	} {
		if err := (Factory{}).ValidateOptions(json.RawMessage(invalid)); err == nil {
			t.Fatalf("invalid options accepted: %s", invalid)
		}
	}
}
