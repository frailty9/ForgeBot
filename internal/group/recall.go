package group

import (
	"context"

	"github.com/soleapi/forgebot/internal/capability"
)

// MessageRecall 是给业务模块使用的“已选好群”的撤回能力。
// 先由 RecallProvider.ForConversation 绑定群，之后每次只传平台消息 ID。
// 它由 runtime.messageRecall 实现，负责检查当前绑定、暂停和取消，再调用
// MessageRecaller 路由；不负责违规判断、用户授权或触发策略。
//
// 一次撤回的调用链（不是调用两次撤回）：
//
//	模块：MessageRecall.Recall(ctx, "42")              // 群已在取得能力时固定
//	宿主路由：MessageRecaller.Recall(ctx, "support", "42") // 补上逻辑群 ID
//	连接器：Recaller.RecallMessage(ctx, "10001", "42")    // 转换为平台群目标
//
// 两个 Recall 方法属于不同接口/实现；前者隐藏路由参数，后者服务多个群。
// 最后只有连接器调用平台 API 执行实际撤回。
type MessageRecall interface {
	Recall(ctx context.Context, messageID string) error
}

// RecallProvider 是业务模块取得 MessageRecall 的入口，由宿主 Runtime 实现。
// conversationID 是配置里的逻辑群 ID（例如 support），不是平台群号。
// 返回的能力固定该群的连接器和目标；群改绑后旧能力会拒绝执行。
// 模块只依赖这个窄接口，无需持有 Runtime、连接器集合或凭据。
type RecallProvider interface {
	ForConversation(conversationID string) (MessageRecall, error)
}

// RecallKey 用于模块声明依赖和解析宿主提供的 RecallProvider。
var RecallKey = capability.NewKey[RecallProvider]("group.message_recall.v1")
