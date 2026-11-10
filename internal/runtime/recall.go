package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/operation"
)

// ForConversation 实现 group.RecallProvider，供模块或其他宿主代码取得能力。
// id 是配置里的逻辑群 ID。此处只保存当前绑定，不发送请求，也不读取 history。
// 返回的 messageRecall 在每次调用时复核绑定，避免热更新改绑后误用旧消息 ID。
func (m *Manager) ForConversation(id string) (group.MessageRecall, error) {
	g := m.acquire()
	if g == nil {
		return nil, operation.New(operation.Unavailable, "runtime is unavailable")
	}
	defer g.mu.RUnlock()
	for _, binding := range g.snapshot.Runtime.Platforms.Groups {
		if binding.ID == id {
			return messageRecall{manager: m, binding: binding}, nil
		}
	}
	return nil, operation.New(operation.NotFound, "conversation is not bound")
}

// messageRecall 是 group.MessageRecall 的宿主实现，一份值只对应一个群。
// binding 保存取得能力时的目标；manager 用于执行时读取当前运行代和路由。
type messageRecall struct {
	manager *Manager
	binding config.GroupBindingConfig
}

// Recall 是业务模块真正调用的方法；群作用域已在 ForConversation 时确定。
// 这里做宿主状态检查，再把群 ID 补回，交给 group.MessageRecaller 继续路由。
// 不按消息年龄或 history 可见性判断能否撤回，实际限制由平台返回。
func (r messageRecall) Recall(ctx context.Context, messageID string) error {
	if strings.TrimSpace(messageID) == "" || len(messageID) > 4096 {
		return operation.New(operation.Invalid, "message id is required and must be at most 4096 bytes")
	}
	if err := ctx.Err(); err != nil {
		return operation.Wrap("message.recall", operation.Canceled, operation.NotStarted, err)
	}
	g := r.manager.acquire()
	if g == nil {
		return operation.New(operation.Unavailable, "runtime is unavailable")
	}
	defer g.mu.RUnlock()
	// 逻辑群名相同不代表目标没变：连接器或平台群目标变化都使旧能力失效。
	matched := false
	for _, binding := range g.snapshot.Runtime.Platforms.Groups {
		if binding.ID == r.binding.ID && binding.ConnectorID == r.binding.ConnectorID && binding.Target == r.binding.Target {
			matched = true
			break
		}
	}
	if !matched {
		return operation.New(operation.Conflict, "conversation binding changed")
	}
	if r.manager.Paused() {
		return operation.New(operation.Disabled, "runtime is paused")
	}
	// 正式运行走当前 ConnectorSet；独立 Sender 构建路径也可提供同样的路由接口。
	// 此处断言的是“多群路由能力”，不是具体平台 Sender 的 Recaller。
	port, ok := g.connectors.(group.MessageRecaller)
	if !ok {
		port, ok = r.manager.builder.Sender.(group.MessageRecaller)
	}
	if !ok {
		return operation.New(operation.Unsupported, "connector does not support message recall")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// 把已绑定的逻辑群 ID 交给路由层；后续由 Registry 转为平台 target。
	err := port.Recall(ctx, r.binding.ID, messageID)
	if err != nil {
		return operation.Wrap("message.recall", operation.Unknown, operation.Describe(err).Effect, err)
	}
	return nil
}
