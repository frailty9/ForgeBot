// Package tooling defines the allow-listed tool boundary used by the bot
// engine. Implementations may be local search tools or external MCP clients.
package tooling

import (
	"context"
	"github.com/soleapi/forgebot/internal/operation"
)

type Definition struct {
	Name        string
	Description string
	InputSchema map[string]any
}

type Call struct {
	Name      string
	Arguments string
}

type Result struct {
	OK      bool               `json:"ok"`
	Code    string             `json:"code,omitempty"`
	Payload string             `json:"payload,omitempty"`
	Failure *operation.Failure `json:"failure,omitempty"`
}

// 工具领域原因码。产生方与 Normalize 必须共用这些常量；写错的码会被归为
// unknown 且执行状态未知，导致无副作用的拒绝被误报为“可能已执行”。
const (
	CodeToolNotAllowed    = "tool_not_allowed"
	CodeModuleDenied      = "module_denied"
	CodeInvalidArguments  = "invalid_arguments"
	CodeInvalidToolName   = "invalid_tool_name"
	CodeBudgetExhausted   = "budget_exhausted"
	CodeSearchTimeout     = "search_timeout"
	CodeSearchUnavailable = "search_unavailable"
)

// Normalize 保留领域原因码，并为失败补充 Core 分类；成功空结果不生成 Failure。
// 未知插件结果默认执行未知，不能自动解释成“没有副作用，可重试”。
func Normalize(result Result) Result {
	if result.OK {
		result.Failure = nil
		return result
	}
	if result.Failure != nil {
		return result
	}
	code, effect := operation.Unknown, operation.EffectUnknown
	switch result.Code {
	case CodeToolNotAllowed, CodeModuleDenied:
		code, effect = operation.PolicyDenied, operation.NotStarted
	case CodeInvalidArguments, CodeInvalidToolName:
		code, effect = operation.Invalid, operation.NotStarted
	case CodeBudgetExhausted:
		code, effect = operation.BudgetExhausted, operation.NotStarted
	case CodeSearchTimeout:
		code, effect = operation.Timeout, operation.NotStarted
	case CodeSearchUnavailable:
		code, effect = operation.Unavailable, operation.NotStarted
	}
	result.Failure = &operation.Failure{Code: code, Operation: "tool.execute", Effect: effect}
	return result
}

// Failed 不把底层诊断原因交给回答模型；远端工具自行返回的业务内容另行保留。
func Failed(reason string, err error) Result {
	failure := operation.Describe(err)
	return Result{Code: reason, Failure: failure, Payload: operation.PublicMessage(failure.Code)}
}

type Tool interface {
	Definition() Definition
	Execute(context.Context, Call) Result
}

type Executor interface {
	Definitions() []Definition
	Execute(context.Context, Call) Result
}
