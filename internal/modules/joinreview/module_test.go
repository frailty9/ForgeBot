package joinreview

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/soleapi/forgebot/internal/modules"
)

// 模块按群提供规则：配置中的群才开启审批；未知字段（含旧的 review_* / auto_review）与无效规则被拒绝。
func TestJoinReviewOptionsProvidePerGroupPolicy(t *testing.T) {
	raw := json.RawMessage(`{"groups":{"support":{"regex":"^.{4,}$","combine":"or","rule":"必须说明来意","model_enabled":true,"reject_reason":"请说明来意"}}}`)
	instance, err := (Factory{}).Build(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	registration := instance.(modules.ExtendedProvider).ExtendedRegistrations().JoinReview
	if registration == nil {
		t.Fatal("join review registration missing")
	}
	policy, ok := registration.Policy("support")
	if !ok || policy.ReviewRegex != "^.{4,}$" || policy.Combine() != "or" || !policy.ModelEnabled() || policy.RejectReason != "请说明来意" {
		t.Fatalf("support policy = %+v %v", policy, ok)
	}
	if _, ok := registration.Policy("other"); ok {
		t.Fatal("group outside options must not be reviewed")
	}
	groups, err := (Factory{}).ReferencedGroups(raw)
	if err != nil || len(groups) != 1 || groups[0] != "support" {
		t.Fatal(groups, err)
	}
	for _, invalid := range []string{
		`{"groups":{"g":{"review_regex":"."}}}`,
		`{"groups":{"g":{"auto_review":true,"regex":"."}}}`,
		`{"groups":{"g":{}}}`,
		`{"groups":{"g":{"regex":"("}}}`,
		`{"groups":{"g":{"regex":".","combine":"xor"}}}`,
		`{"groups":{" ":{"regex":"."}}}`,
		`{"unknown":true}`,
	} {
		if err := (Factory{}).ValidateOptions(json.RawMessage(invalid)); err == nil {
			t.Fatalf("invalid options accepted: %s", invalid)
		}
	}
}
