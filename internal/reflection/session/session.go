// Package session 定义沉淀流水线所需的答题材料，不持有消息历史或调度状态。
package session

import (
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
)

// AnswerSession 是一个已归组的问题及管理员回答快照。
type AnswerSession struct {
	Key       string
	Question  messaging.InboundMessage
	Answers   []messaging.InboundMessage
	StartedAt time.Time
	LastAt    time.Time
}
