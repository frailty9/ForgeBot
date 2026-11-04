package runtime

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/secret"
)

// 每代持有自己的只读任务/供应商快照，消费者通过 capability 复用同一服务。
func (b Builder) withDecision(ctx context.Context, snapshot config.Snapshot, client llm.Client, secretRef, secretValue string) (Builder, error) {
	if b.Decisions != nil {
		return b, nil
	}
	opts := decision.Options{MaxConcurrent: snapshot.Runtime.Decision.MaxConcurrent, Logger: b.Logger}
	var err error
	opts.DMTimeout, opts.LLMTimeout, err = snapshot.Runtime.Decision.Timeouts()
	if err != nil {
		return b, err
	}
	if client != nil {
		opts.LLM = decision.LLMProvider{Client: client, Model: snapshot.Runtime.LLM.Model}
	}
	if dm := snapshot.Runtime.Decision.DM; snapshot.Runtime.Decision.DMEnabled() {
		key := ""
		if secretRef == dm.APIKeyRef {
			key = secretValue
		} else if b.Secrets != nil {
			key, err = b.Secrets.Get(ctx, dm.APIKeyRef)
			if err != nil && !errors.Is(err, secret.ErrNotFound) {
				return b, err
			}
		}
		threshold := 0.6
		if dm.MinConfidence != nil {
			threshold = *dm.MinConfidence
		}
		opts.DM = decision.JevProvider{Client: &http.Client{Timeout: opts.DMTimeout}, Endpoint: dm.Endpoint, APIKey: key, Model: dm.Model, MinConfidence: threshold}
	}
	scope := strings.TrimSpace(snapshot.Runtime.AnswerPolicy.Scope)
	if scope == "" {
		scope = "未限制专业主题，允许普通有效请求；仍区分日常互动和主动答复必要性。"
	}
	b.Decisions, err = decision.New(decision.BuiltinTasks(scope), opts)
	return b, err
}
