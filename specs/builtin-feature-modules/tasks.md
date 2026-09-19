# 内置功能模块化与 Runtime 配置重组实施计划

依据：requirements.md 与 design.md。

状态：任务已于 2026-09-19 确认；第 1 项基础运行时能力已完成，开始进入第 2 项。

## 1. 扩展内置模块运行时与能力注册

- [x] 1.1 新增受限 capability 容器、稳定能力 ID、类型解析和依赖拓扑校验。
- [x] 1.2 将 modules.Descriptor、Factory、Instance 和注册集合扩展为 Tool、Command、
      Observer、Background Job、Status 及 capability provider/consumer。
- [x] 1.3 实现候选 Set 的 Prepare、Start、Retire、Close 生命周期和逆序资源清理。
- [ ] 1.4 将通用 Tool 目录编译移入模块 Set，拒绝重复工具名；Engine 只消费通用
      Tool 定义和执行器，不保留业务工具名特判。
- [x] 1.5 增加模块状态、构建错误、依赖错误和生命周期测试；保持现有
      native.reply_experience 行为。
- Requirement: R4, R5, R6, R7

## 2. 拆分核心 Admission 与主动介入模块

- [x] 2.1 从 internal/bot 和 internal/gate 拆出核心 Admission：输入合法性、消息
      ID 去重、身份/寻址前置约束和命令优先边界不可关闭。
- [x] 2.2 新增 native.proactive_reply，迁移有界缓存、静默窗口、管理员引用取消、
      群组开关、flush 和 DeferredResponder。
- [x] 2.3 删除 platforms.groups[].auto_reply_unmentioned，将群级开关迁入模块 options；
      删除未生效的 max_count。
- [x] 2.4 将管理命令从 /gate 重命名为 /proactive，状态和统计归模块 capability；
      删除旧 Gate HTTP 统计路径。
- [x] 2.5 增加关闭模块、群未启用、未 @ 不调用 LLM、明确 @/命令不受影响、管理员
      取消和 generation 退场测试。
- Requirement: R1, R5, R7

## 3. 将知识检索迁入内置 Tool 插件

- [ ] 3.1 新增 native.structured_search 工厂和严格 options，索引构建与资源所有权
      迁入模块实例。
- [ ] 3.2 将 search_docs 作为 Tool Registration 挂载到宿主通用 LLM Tool 目录；
      Engine 删除 Retriever、SearchPolicy 和工具名称特判。
- [ ] 3.3 将 /search 作为内置模块 Command Registration，复用 Search capability；
      关闭模块时不注册命令、工具或索引。
- [ ] 3.4 提供 search.control 和 knowledge.publisher_factory capability，迁移
      Reload、Status 和反思所需的受控文档发布入口。
- [ ] 3.5 回归普通消息不预检索、工具预算、显式 /search、无 LLM 回退、重载失败保留
      旧索引及 Markdown/MDX/TXT 解析行为。
- Requirement: R2, R4, R5, R7

## 4. 将知识反思迁入观察器与后台任务模块

- [ ] 4.1 新增 native.knowledge_reflection 工厂，迁移 buffer、scanner、reflector、
      pipeline 的组装与配置校验。
- [ ] 4.2 以只读 Observer 接收入站必要字段，以 Background Job 在激活后启动定时扫描；
      准备阶段不得缓存、写文件或发送通知。
- [ ] 4.3 通过 agent.completer、knowledge.publisher_factory 和 search.control
      capability 解耦 LLM、文档发布和索引重载。
- [ ] 4.4 通过 reflection.control 提供手动扫描和脱敏状态；关闭模块时完全停止观察、
      扫描、发布和重载。
- [ ] 4.5 回归引用配对、置信度、去重、原子 Markdown、每批一次 Reload、扫描互斥和
      退场取消。
- Requirement: R3, R4, R5, R7

## 5. 收口 Runtime 配置、管理状态和存储流程

- [ ] 5.1 将 RuntimeConfig 改为核心 admission 加 modules，删除顶层 gate、search、
      reflection 和群组级主动回复字段；严格拒绝旧字段。
- [ ] 5.2 删除 Search policy/backend、Reflection mode 和 Gate max_count，将模块 options
      的校验迁移到对应工厂。
- [ ] 5.3 统一模块变更为 replace_modules 候选 generation，保留 JSON Store 的版本、
      回滚、热更新和失败保活语义；不实现 DBStore 或拆分 JSON 文件。
- [ ] 5.4 将管理 API、运行状态和 /status 改为模块状态详情；保留 Search Reload 和
      Reflection Scan HTTP 路径但改走 capability。
- [ ] 5.5 更新示例 JSON、YAML、配置文档、架构文档、管理命令文档和测试夹具。
- Requirement: R5, R6, R7

## 6. 收尾验证与提交

- [ ] 6.1 每个阶段形成独立中文说明、英文类型前缀的 Git 提交，保证中间提交可编译。
- [ ] 6.2 执行定向模块测试、go test ./...、go vet ./... 和竞态相关测试。
- [ ] 6.3 检查旧配置字段、Gate/Search/Reflection 核心特判、重复实现和外部插件接口
      是否仍被意外保留。
- [ ] 6.4 更新本规格 tasks 状态、架构交接记录和最终迁移说明。
- Requirement: R1–R7

## 提交建议

1. feat: 扩展功能模块运行时与能力注册
2. refactor: 拆分核心准入与主动介入模块
3. refactor: 将知识检索迁入内置工具插件
4. refactor: 将知识反思迁入内置模块
5. refactor: 收口运行时模块配置与管理状态
6. docs: 更新内置模块使用与迁移说明
