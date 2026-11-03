# Core 操作结果

Core 边界使用稳定分类表达失败，保留领域结果与 Go error 链。程序通过 `operation.Describe`、`errors.Is/As` 判断，不匹配人类文案。重复、忽略、缓冲、被策略抑制以及成功的空检索结果仍是正常结果。

管理 API 和测试消息入口的失败响应包含公开 `error` 与结构化 `failure`，工具失败也携带相同语义。底层诊断留在日志，HTTP 不再把所有内部失败都当成 400。例子：

```json
{
  "error": "操作超时",
  "failure": {
    "code": "timeout",
    "operation": "message.send",
    "effect": "unknown"
  }
}
```

分类包含 invalid、not_configured、disabled、unsupported、unauthenticated、permission_denied、policy_denied、not_found、expired、conflict、rate_limited、budget_exhausted、busy、unavailable、timeout、canceled、internal、unknown。

`effect` 独立表达本次操作的副作用：`not_started`、`completed`、`partial` 或 `unknown`。发送请求后超时或响应无法解码时，结果可能未知；不能据此自动重发。第三方未提供可靠状态时保留 unknown，不根据供应商自由文本或任意 retcode 猜测权限、执行状态。

该契约供当前和后续 Core 功能复用，不要求所有内部辅助函数返回同一个大结构。群管理、存储和 CLI 的新增路径随各自工作包接入。
