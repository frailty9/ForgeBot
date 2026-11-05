package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/tooling"
)

// ExecuteTool 由宿主提供，负责同一白名单及每次 Hook/授权/进度效果。
// Loop 不拥有发消息、修改配置或访问凭据的权限。
type ExecuteTool func(context.Context, tooling.Call) (tooling.Result, error)

type Loop struct {
	Client      llm.Client
	Limits      Limits
	ToolBudgets map[string]int
	Execute     ExecuteTool
}

// Run 只返回最终回答。中间工具请求与输出不会作为聊天消息发出。
// 每轮输入是独立快照，取消/超限立即结束，不启动无法取消的后台补救请求。
func (l Loop) Run(ctx context.Context, request llm.Request) (llm.Response, error) {
	limits := l.Limits.Defaults()
	if err := limits.Validate(); err != nil {
		return llm.Response{}, operation.Wrap("agent.configure", operation.Invalid, operation.NotStarted, err)
	}
	if l.Client == nil {
		return llm.Response{}, operation.New(operation.NotConfigured, "answer LLM is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()
	messages := append([]llm.Message(nil), request.Messages...)
	definitions := append([]llm.ToolDefinition(nil), request.Tools...)
	allowed := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		allowed[definition.Name] = true
	}
	used := make(map[string]int)
	ids := make(map[string]bool)
	total, contextBytes := 0, 0
	effect := operation.NotStarted
	fail := func(code operation.Code, message string) (llm.Response, error) {
		return llm.Response{}, operation.Wrap("agent.run", code, effect, operation.New(code, message))
	}
	for turn := 0; turn < limits.MaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return llm.Response{}, operation.Wrap("agent.run", operation.Canceled, effect, err)
		}
		request.Messages = append([]llm.Message(nil), messages...)
		request.Tools = nil
		if turn < limits.MaxTurns-1 && total < limits.MaxToolCalls && l.Execute != nil {
			for _, definition := range definitions {
				budget := l.ToolBudgets[definition.Name]
				if budget <= 0 {
					budget = limits.MaxToolCalls
				}
				if used[definition.Name] < budget {
					request.Tools = append(request.Tools, definition)
				}
			}
		}
		response, err := l.Client.Complete(ctx, request)
		if err != nil {
			return llm.Response{}, operation.Wrap("agent.model", operation.Unknown, effect, err)
		}
		if err := ctx.Err(); err != nil {
			return llm.Response{}, operation.Wrap("agent.run", operation.Canceled, effect, err)
		}
		calls := response.Message.ToolCalls
		if len(calls) == 0 {
			if response.Text == "" {
				response.Text = response.Message.Text()
			}
			return response, nil
		}
		if turn == limits.MaxTurns-1 || len(calls) > limits.MaxToolsPerTurn || len(calls) > limits.MaxToolCalls-total {
			return fail(operation.BudgetExhausted, "agent tool or turn budget exhausted")
		}
		// 整批先验证身份，任何调用执行前拒绝重复/缺失 ID，防止结果串联。
		for _, call := range calls {
			if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || ids[call.ID] {
				return fail(operation.Invalid, "invalid or repeated tool call identity")
			}
			ids[call.ID] = true
		}
		assistant := llm.AssistantToolCallMessage(response.Message)
		encoded, err := json.Marshal(assistant)
		if err != nil {
			return fail(operation.Internal, "encode tool calls failed")
		}
		contextBytes += len(encoded)
		if contextBytes > limits.MaxContextBytes {
			return fail(operation.BudgetExhausted, "agent intermediate context exhausted")
		}
		messages = append(messages, assistant)
		for _, call := range calls {
			if err := ctx.Err(); err != nil {
				return llm.Response{}, operation.Wrap("agent.run", operation.Canceled, effect, err)
			}
			total++
			result := tooling.Result{Code: "tool_not_allowed"}
			budget := l.ToolBudgets[call.Name]
			if budget <= 0 {
				budget = limits.MaxToolCalls
			}
			if allowed[call.Name] && l.Execute != nil {
				if used[call.Name] >= budget {
					result = tooling.Result{Code: "budget_exhausted"}
				} else {
					used[call.Name]++
					result, err = l.Execute(ctx, tooling.Call{Name: call.Name, Arguments: call.Arguments})
					// 通用 Tool 没有声明无副作用，调用后不得假定“可以安全重试”。
					effect = operation.EffectUnknown
					if err != nil {
						return llm.Response{}, operation.Wrap("agent.tool", operation.Unknown, effect, err)
					}
				}
			}
			encoded, err = json.Marshal(tooling.Normalize(result))
			if err != nil {
				return fail(operation.Internal, "encode tool result failed")
			}
			if len(encoded) > limits.MaxResultBytes {
				// 用完整合法的 JSON 失败信封替换超限结果，不能截断成误导性证据。
				encoded, _ = json.Marshal(tooling.Normalize(tooling.Result{Code: "budget_exhausted", Payload: fmt.Sprintf("tool result exceeds %d bytes", limits.MaxResultBytes)}))
			}
			message := llm.ToolResultMessage(call.ID, call.Name, string(encoded))
			wire, _ := json.Marshal(message)
			contextBytes += len(wire)
			if contextBytes > limits.MaxContextBytes {
				return fail(operation.BudgetExhausted, "agent intermediate context exhausted")
			}
			messages = append(messages, message)
		}
	}
	return fail(operation.BudgetExhausted, "agent turn budget exhausted")
}
