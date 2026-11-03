// Package operation 定义 Core 边界的失败语义。领域的正常空结果、忽略、
// 去重与缓冲仍由各自结果类型表达，不应为了统一而制造 error。
package operation

import (
	"context"
	"errors"
	"io/fs"
	"net"
)

// Code 是供程序判断的稳定分类，不能依靠供应商错误文案推测。
type Code string

const (
	Invalid          Code = "invalid"
	NotConfigured    Code = "not_configured"
	Disabled         Code = "disabled"
	Unsupported      Code = "unsupported"
	Unauthenticated  Code = "unauthenticated"
	PermissionDenied Code = "permission_denied"
	PolicyDenied     Code = "policy_denied"
	NotFound         Code = "not_found"
	Expired          Code = "expired"
	Conflict         Code = "conflict"
	RateLimited      Code = "rate_limited"
	BudgetExhausted  Code = "budget_exhausted"
	Busy             Code = "busy"
	Unavailable      Code = "unavailable"
	Timeout          Code = "timeout"
	Canceled         Code = "canceled"
	Internal         Code = "internal"
	Unknown          Code = "unknown"
)

// Effect 表示这次边界操作的副作用确定性，与失败分类相互独立。
// 网络超时只能说明未收到确认，不能据此声称远端没有执行。
type Effect string

const (
	NotStarted    Effect = "not_started"
	Completed     Effect = "completed"
	Partial       Effect = "partial"
	EffectUnknown Effect = "unknown"
)

// Failure 可安全用于 API/工具输出；诊断 cause 不在此结构中序列化。
type Failure struct {
	Code      Code   `json:"code"`
	Operation string `json:"operation,omitempty"`
	Effect    Effect `json:"effect"`
}

// Error 保留领域错误链；外层边界声明自己的副作用，不继承内层操作的状态。
type Error struct {
	Failure
	message string
	cause   error
}

func (e *Error) Error() string {
	if e.message != "" {
		return e.message
	}
	if e.cause != nil {
		return e.cause.Error()
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error { return e.cause }

func New(code Code, message string) *Error {
	return &Error{Failure: Failure{Code: code, Effect: NotStarted}, message: message}
}

// Wrap 仅增加边界语义，不丢弃 errors.Is/As 可访问的原始原因。
// fallback 只用于原因尚未分类时；已知原因（包括 context）优先。
func Wrap(name string, fallback Code, effect Effect, cause error) error {
	if cause == nil {
		return nil
	}
	code := Describe(cause).Code
	if code == Unknown && fallback != "" {
		code = fallback
	}
	return &Error{Failure: Failure{Code: code, Operation: name, Effect: effect}, cause: cause}
}

// Describe(nil) 返回 nil，避免把正常结果表现为一个“未知错误”。
func Describe(err error) *Failure {
	if err == nil {
		return nil
	}
	var typed *Error
	if errors.As(err, &typed) {
		copy := typed.Failure
		return &copy
	}
	code := Unknown
	var network net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code = Timeout
	case errors.Is(err, context.Canceled):
		code = Canceled
	case errors.Is(err, fs.ErrPermission):
		code = PermissionDenied
	case errors.Is(err, fs.ErrNotExist):
		code = NotFound
	case errors.As(err, &network):
		if network.Timeout() {
			code = Timeout
		} else {
			code = Unavailable
		}
	}
	// 原始 error 未声明副作用，不能替调用者假定尚未执行。
	return &Failure{Code: code, Effect: EffectUnknown}
}

// HTTPCode 只映射标准 HTTP 状态，不解析不稳定的供应商响应正文。
func HTTPCode(status int) Code {
	switch status {
	case 400, 422:
		return Invalid
	case 401:
		return Unauthenticated
	case 403:
		return PermissionDenied
	case 404:
		return NotFound
	case 408, 504:
		return Timeout
	case 409:
		return Conflict
	case 410:
		return Expired
	case 429:
		return RateLimited
	case 501:
		return Unsupported
	case 500, 502, 503:
		return Unavailable
	default:
		return Unknown
	}
}

// PublicMessage 是稳定的公开提示，详细响应与底层地址只进入诊断日志。
func PublicMessage(code Code) string {
	switch code {
	case Invalid:
		return "请求无效"
	case NotConfigured:
		return "所需能力尚未配置"
	case Disabled:
		return "所需能力已禁用"
	case Unsupported:
		return "当前不支持此操作"
	case Unauthenticated:
		return "认证失败"
	case PermissionDenied:
		return "没有执行此操作的权限"
	case PolicyDenied:
		return "当前策略不允许此操作"
	case NotFound:
		return "目标不存在"
	case Expired:
		return "目标已失效"
	case Conflict:
		return "状态冲突，请刷新后再试"
	case RateLimited:
		return "请求过于频繁"
	case BudgetExhausted:
		return "处理预算已耗尽"
	case Busy:
		return "系统繁忙"
	case Unavailable:
		return "服务暂时不可用"
	case Timeout:
		return "操作超时"
	case Canceled:
		return "操作已取消"
	default:
		return "操作失败，请查看诊断日志"
	}
}
