# Implementation Plan

- [x] 1. 拆分静态配置与运行时配置及其配置源
  - 定义 `StaticConfig`、`RuntimeConfig`、带版本和时间戳的快照。
  - 增加启动 YAML、静态环境变量覆盖与 JSON 热更新配置加载。
  - 定义配置源优先级，禁止环境变量覆盖热更新字段。
  - 保持现有示例配置兼容迁移路径。
  - 从普通配置模型移除 API Key 正文，并拒绝 JSON 中的 `api_key` 字段。
  - _Requirement: 1, 2, 3, 8, 13, 14_

- [x] 2. 实现独立只写 SecretStore
  - 定义 SecretStore 接口、密钥引用、版本和非敏感状态。
  - 增加供管理层调用的 LLM API Key 独立写入与显式删除能力；HTTP 端点在管理 API 任务中接入认证后开放。
  - 保证读取 API、日志、审计和普通配置历史不包含密钥正文。
  - 支持新 client 验证成功后切换，失败保留旧密钥版本。
  - _Requirement: 2, 5, 12, 13, 14_

- [x] 3. 实现版本化 JSON 热更新 Store
  - 定义 `Store`、更新请求、历史摘要和冲突错误。
  - 实现基于临时文件 + 原子替换的 `JSONFileStore`。
  - 保存最近 JSON 热更新历史并支持回滚。
  - 确保 Store 不会写回静态 YAML 或环境变量。
  - _Requirement: 1, 3, 9, 10_

- [x] 4. 抽离 Bot Engine 的运行时依赖
  - 让 Engine 通过依赖结构接收 LLM、sender 和策略。
  - 禁止 Engine 自行构造 LLM client 或读取配置文件。
  - 为 fake LLM 增加测试接缝。
  - _Requirement: 5, 6_

- [x] 5. 实现 Runtime Manager
  - 实现运行状态、单写应用锁和变更影响判定。
  - 支持日志 handler 原地更新，并支持 Gate/Bot 受控重建。
  - 支持 LLM client 创建成功后的原子替换。
  - 支持组件软重启、优雅停止、超时和失败恢复。
  - _Requirement: 4, 5, 6, 7, 11_

- [x] 6. 接入管理 API
  - 增加当前配置、历史、校验、更新、回滚和 runtime 状态接口。
  - 增加 LLM API Key 独立只写与显式删除接口，读取响应仅返回非敏感状态。
  - 实现版本冲突、静态配置拒绝和明确错误响应。
  - 在路由层预留认证中间件边界。
  - _Requirement: 2, 3, 8, 9, 10, 11_

- [x] 7. 增加端到端测试与文档
  - 覆盖 LLM 供应商切换、配置回滚、软重启和失败保活。
  - 更新 README、架构文档和路线图。
  - 明确哪些配置立即生效、哪些需要软重启、哪些需要进程重启。
  - _Requirement: 1-14_
