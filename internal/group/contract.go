// Package group 定义协议无关的群事件和可选群操作。支持状态、账号权限、
// 功能配置和实际执行结果独立表达，未支持的连接器无需实现空方法。
package group

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/messaging"
)

// ID 由有序的字段生成稳定的事件标识，避免拼接歧义。
func ID(parts ...string) string {
	data, _ := json.Marshal(parts)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

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

// EventReply 是模块对群事件的受限回复意图。Text 是完整的纯文本形式；Segments 非空时
// 支持结构化段的连接器按段发送（如 @ 成员），其余连接器发送 Text。
type EventReply struct {
	Text     string
	Segments []messaging.Segment
}
type EventHandler interface {
	HandleGroupEvent(context.Context, Event) (EventResult, error)
}

type ReviewRequest struct {
	ConnectorID string
	Reference   string
	Approve     bool
	Reason      string
	// ExpectedPolicy 由自动审批宿主设置，在实际执行的运行代内再次核对。
	ExpectedPolicy *Policy
}
type Reviewer interface {
	ReviewGroupRequest(context.Context, string, ReviewRequest) error
}

// Recaller 是具体平台发送器可选实现的撤回接口，例如 OneBot Sender。
// target 已由 Registry 从逻辑群转换为平台群标识（QQ 群号、group_openid 等）；
// messageID 是该平台的消息 ID。此层把请求转换为 delete_msg 等真实平台调用。
// 连接器不支持撤回时无需实现它，Registry 会返回 unsupported。
type Recaller interface {
	RecallMessage(ctx context.Context, target, messageID string) error
}

// MessageRecaller 是连接器集合内部的多群路由接口，由 platform.Set/Registry 实现。
// conversationID 是逻辑群 ID，Registry 据此查到具体连接器和平台 target，
// 然后调用该连接器的 Recaller.RecallMessage；它本身不直接请求平台 API。
//
// 它与面向业务模块的 MessageRecall 都有 Recall 方法，但参数和职责不同：
// MessageRecall 已绑定群，只收 messageID；此接口服务多个群，必须再收群 ID。
// 业务模块应从 RecallKey 取得 RecallProvider，而不是直接依赖此路由接口。
type MessageRecaller interface {
	Recall(ctx context.Context, conversationID, messageID string) error
}

// ReviewReasonReporter 是可选能力；未声明时拒绝仍执行，只忽略理由。
type ReviewReasonReporter interface {
	GroupReviewReasonSupported() bool
}
type Support struct {
	JoinEvents       bool   `json:"join_events"`
	MemberEvents     bool   `json:"member_events"`
	JoinReview       bool   `json:"join_review"`
	JoinReviewReason bool   `json:"join_review_reason"`
	MessageRecall    bool   `json:"message_recall"`
	Permission       string `json:"permission"`
}

type Actions interface {
	// Support 只读本地适配能力元数据，不主动探测远端权限或触发网络请求。
	Support(context.Context, string) (Support, error)
	Review(context.Context, string, ReviewRequest) error
}

// EventReporter 仅声明适配器实现了哪些事件，不保证远端服务已开启上报。
type EventReporter interface {
	GroupEventSupport() (joinRequests, memberJoined bool)
}

// Policy 是一个群的入群自动审批规则，由 native.join_review 模块按群提供。
// 模块启用且该群在其配置中即表示开启自动审批，宿主不再另设开关。
type Policy struct {
	ReviewRule         string
	ReviewRegex        string
	ReviewModelEnabled *bool
	ReviewCombine      string
	RejectReason       string
	ExternalReview     *ExternalReview
}

// ReviewTarget 是宿主对某个逻辑群的审批视图：Bound 表示群已绑定，
// Enabled 表示审批模块为该群提供了策略；判断仍由宿主执行。
type ReviewTarget struct {
	Bound   bool
	Enabled bool
	Policy  Policy
	Decider decision.Decider
}
