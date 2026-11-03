package bot

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/messaging"
)

type answerGate struct {
	stop, mixed, evidence bool
	reply                 string
}

// @ 只改变寻址资格，不能跳过已配置的业务范围。判断失败时直接澄清或沉默，
// 不将“无法确认”交给普通回答模型自由决定是否越界。
func (e *Engine) decideAnswer(ctx context.Context, in messaging.InboundMessage, text string) (answerGate, error) {
	proactive := runFrom(ctx).info.Trigger == "proactive"
	if strings.TrimSpace(e.deps.Policy.Scope) == "" && !proactive && e.deps.Policy.EvidenceTool == "" {
		return answerGate{}, nil
	}
	answer := decision.Answer{}
	fullInput := len(text) <= 6000 && (in.Quoted == nil || len(in.Quoted.Text) <= 3000)
	// 被裁掉的当前/引用内容未获范围判断，不能给前缀判定后放行完整请求。
	if e.deps.Decisions != nil && fullInput {
		history := runFrom(ctx).history
		contextText := make([]map[string]string, 0, 4)
		if len(history) > 4 {
			history = history[len(history)-4:]
		}
		for _, message := range history {
			contextText = append(contextText, map[string]string{"role": string(message.Role), "text": boundedDecisionText(message.Text(), 512)})
		}
		quoted := ""
		if in.Quoted != nil {
			quoted = boundedDecisionText(in.Quoted.Text, 3000)
		}
		state, _ := json.Marshal(map[string]any{"message": boundedDecisionText(text, 6000), "quoted": quoted, "context": contextText, "proactive": proactive, "trusted_administrator": in.Administrator})
		request := decision.Request{TaskID: decision.AnswerTaskID, Version: 1, State: state}
		if e.deps.Policy.ImageUnderstanding {
			for _, part := range userMessage(in, text, false).Content {
				if part.Type == "image" {
					request.Media = append(request.Media, part)
				}
			}
		}
		result, err := e.deps.Decisions.Decide(ctx, request)
		if err != nil {
			if ctx.Err() != nil {
				return answerGate{}, ctx.Err()
			}
			e.logger.WarnContext(ctx, "answer decision unavailable", "err", err)
		} else {
			answer = result.Answer
			e.logger.DebugContext(ctx, "answer decision finished", "source", result.Source, "reason", result.Reason, "scope", answer["scope"], "respond", answer["respond"], "evidence", answer["evidence"])
		}
	}
	if proactive && answer["respond"] != "yes" {
		return answerGate{}, errModuleStopped
	}
	gate := answerGate{evidence: answer["evidence"] == "required"}
	if strings.TrimSpace(e.deps.Policy.Scope) == "" {
		return gate, nil
	}
	switch answer["scope"] {
	case "in_domain":
		return gate, nil
	case "daily":
		if e.deps.Policy.AllowDaily {
			return gate, nil
		}
	case "mixed":
		gate.mixed = true
		return gate, nil
	case "outside":
	default:
		if proactive {
			return answerGate{}, errModuleStopped
		}
		text := strings.TrimSpace(e.deps.Policy.ClarificationText)
		if text == "" {
			text = "请说明这个问题与当前服务的关系，或补充必要信息。"
		}
		return answerGate{stop: true, reply: text}, nil
	}
	if proactive {
		return answerGate{}, errModuleStopped
	}
	text = strings.TrimSpace(e.deps.Policy.RefusalText)
	if text == "" {
		text = "这个专业问题不在当前支持范围内，请咨询对应领域的支持。"
	}
	return answerGate{stop: true, reply: text}, nil
}

func boundedDecisionText(text string, max int) string {
	if len(text) <= max {
		return text
	}
	text = text[:max]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text + "[已截断]"
}
