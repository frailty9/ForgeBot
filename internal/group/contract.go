// Package group 定义协议无关的群事件和可选群操作。支持状态、账号权限、
// 功能配置和实际执行结果独立表达，未支持的连接器无需实现空方法。
package group

import (
	"context"
	"time"

	"github.com/soleapi/forgebot/internal/operation"
)

type EventKind string

const (
	JoinRequested EventKind = "join_requested"
	MemberJoined  EventKind = "member_joined"
)

type Event struct {
	ID             string    `json:"id"`
	Kind           EventKind `json:"kind"`
	ConversationID string    `json:"conversation_id"`
	ConnectorID    string    `json:"connector_id"`
	UserID         string    `json:"user_id"`
	UserName       string    `json:"user_name,omitempty"`
	Comment        string    `json:"comment,omitempty"`
	Reference      string    `json:"-"`
	At             time.Time `json:"at"`
	Self           bool      `json:"-"`
	Historical     bool      `json:"-"`
}
type EventResult struct {
	Accepted bool   `json:"accepted"`
	Verdict  string `json:"verdict"`
}

type EventReply struct{ Text string }
type EventHandler interface {
	HandleGroupEvent(context.Context, Event) (EventResult, error)
}

type ReviewRequest struct {
	ConnectorID string
	Reference   string
	Approve     bool
	Reason      string
}
type Reviewer interface {
	ReviewGroupRequest(context.Context, string, ReviewRequest) error
}
type Announcer interface {
	PublishGroupAnnouncement(context.Context, string, string) error
}
type Recaller interface {
	RecallGroupMessage(context.Context, string, string) error
}
type Support struct {
	JoinReview   bool   `json:"join_review"`
	Announcement bool   `json:"announcement"`
	Recall       bool   `json:"recall"`
	Permission   string `json:"permission"`
}
type Actions interface {
	Support(context.Context, string) (Support, error)
	Review(context.Context, string, ReviewRequest) error
	Announce(context.Context, string, string) error
	Recall(context.Context, string, string) error
}

type Policy struct {
	ReviewEnabled bool   `json:"review_enabled"`
	AutoReview    bool   `json:"auto_review"`
	ReviewRule    string `json:"review_rule,omitempty"`
	Announcements bool   `json:"announcements"`
	Recall        bool   `json:"recall"`
}

type Status string

const (
	Pending   Status = "pending"
	Executing Status = "executing"
	Done      Status = "completed"
	Failed    Status = "failed"
	Uncertain Status = "unknown"
)

// Record 是运营聚合；Reference 是连接器私有句柄，只在仓储和操作路由间传递。
// Revision 用于条件更新，fingerprint 防止相同幂等键携带不同动作内容。
type Record struct {
	SchemaVersion  int                `json:"schema_version"`
	ID             string             `json:"id"`
	Revision       uint64             `json:"revision"`
	Kind           string             `json:"kind"`
	ConversationID string             `json:"conversation_id"`
	ConnectorID    string             `json:"connector_id,omitempty"`
	UserID         string             `json:"user_id,omitempty"`
	Comment        string             `json:"comment,omitempty"`
	Reference      string             `json:"reference,omitempty"`
	Fingerprint    string             `json:"fingerprint,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	ExpiresAt      time.Time          `json:"expires_at,omitempty"`
	Status         Status             `json:"status"`
	Failure        *operation.Failure `json:"failure,omitempty"`
	Decision       string             `json:"decision,omitempty"`
}

func (r Record) Public() Record { r.Reference = ""; r.Fingerprint = ""; return r }

// Store 是单后端提供的运营仓储。Create 返回 existing/new，Update 是 CAS，
// 不暴露文件路径或事务对象；状态提交后才允许执行外部副作用。
type Store interface {
	Create(context.Context, Record) (Record, bool, error)
	Get(context.Context, string) (Record, error)
	List(context.Context, int) ([]Record, error)
	Update(context.Context, uint64, Record) (Record, error)
}
