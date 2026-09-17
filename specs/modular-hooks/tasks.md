# 模块级声明式 Hook 挂载实施计划

日期：2026-09-17。

状态：任务 1–7 已完成，普通验证通过；竞态验证受本地工具链阻塞，详见记录。工作分支 feat/modular-hooks，基线 master@a8f63ed；尚未提交或合并。

实施基线：[requirements.md](requirements.md)、[design.md](design.md)。每完成一个任务更新勾选、验证结果和阶段交接，不跨任务提前扩大范围。

- [x] 1. 增加模块配置模型并守住 Store 边界
  - 在 RuntimeConfig 中增加模块级配置 envelope，以 module_id 为键，包含 enabled 与有界 options；默认配置启用 native.reply_experience，旧运行时 JSON 缺少 modules 时保持现有行为。
  - 区分“字段缺省”与显式 enabled=false，禁用时保留 options；校验 ID、数量、单项及总 options 大小。
  - 在 app 组合根注入内置模块目录，由纯校验入口验证已知模块 options；未知模块在本期明确报错。
  - 保持 settings.Store 和 JSONFileStore，不新增平行 ConfigStore、DBStore、配置拆文件或 PATCH API。
  - 修复/验证模块 map 与 RawMessage 在 Current、RuntimeAt、Update、History、Rollback 和 Runtime 快照间的深拷贝隔离。
  - 更新脱敏公共配置，仅暴露已知原生模块允许公开的非敏感 options。
  - 测试默认兼容、显式禁用、未知字段/模块、大小边界、版本冲突、历史回滚和 fake Store 接缝。
  - _Requirement: R2, R5, R6, R9_

- [x] 2. 实现强类型 Hook 契约与不可变执行链
  - 新增 internal/hooks，定义 ExecutionContext、agent.input.prepare、tool.before_execute、reply.before_send 的独立输入、结果、处理器与注册类型。
  - 实现受保护字段隔离、输入深复制、互斥动作校验和工作副本提交；不提供万能 map、JSON Patch 或任意宿主方法。
  - 实现确定性排序：source_rank、host_order、module_id、handler_id；技术注册标识重复应使候选构建失败，不检查业务相似或替代完整性。
  - 实现终态传播：stop/deny/suppress 不可被后续处理器恢复；父 context 取消或 deadline 时停止链，过期结果不应用。
  - 实现 panic 转换、处理时长和结果分类；原生默认错误策略为继续，但非法越权结果停止本次处理。
  - 加入模块/处理器数量上限和内部超时默认值；同步调用不通过遗留 goroutine 伪造超时隔离。
  - 为空链、排序、结果合并、受保护字段、panic、取消、deadline、并发隔离增加单测和微基准。
  - _Requirement: R1, R3, R4, R7, R8, R9_

- [x] 3. 实现模块目录、实例集合与资源所有权
  - 新增 internal/modules，定义 Descriptor、Factory、Instance、Registrations 和 ModuleSet；app 显式注册内置工厂，不使用 init() 全局发现。
  - 由宿主赋予 native/external 来源，模块不能自行提升层级；本期 external 仅作为测试替身，不接入 FPP。
  - 按配置构建模块实例、收集全部注册、一次性编译 Chain；任一步失败按所有权逆序清理候选资源。
  - Engine 构建成功后统一拥有 ModuleSet 关闭回调；构建失败由 Builder 关闭，generation 不重复释放。
  - Close 必须幂等、有界；用资源型测试模块验证构建失败、Engine shutdown、重复关闭和反向清理。
  - 明确拒绝本期不支持的命令、Job 和异步观察注册，不能静默忽略。
  - _Requirement: R1, R3, R4, R6, R7, R8_

- [x] 4. 将 Hook Chain 接入 Bot Engine 主流程
  - 在普通问答显式 /search 分流之后、首次 LLM 请求之前执行 agent.input.prepare；所有模块结束后仍无文本和媒体时返回 empty_input，不调用 LLM、不记录为 ERROR。
  - 在工具白名单、功能开关和预算通过后执行 tool.before_execute；拒绝时生成保留 call ID 的正常工具结果，不执行工具。
  - 仅在工具链整体通过后执行受限 ProgressEffect；发送失败记录警告但不阻断工具，一次 run 最多尝试一次进度提示。
  - 在 Engine 的普通回复、Gate 主动回复和进度提示发送前执行 reply.before_send；管理命令和运维通知明确不在本期覆盖。
  - 为直接请求和 Gate flush 创建独立 run，确保同一 run 共享效果去重状态，不用模块全局变量。
  - 保持当前一次工具回合，不顺便实现有界 Agent Loop；保持工具定义、权限和预算边界不变。
  - 测试空链兼容、三条回复路径、多个工具调用、拒绝工具、进度提示失败、两群并发和取消传播。
  - _Requirement: R1, R3, R6, R7, R9_

- [x] 5. 迁移 native.reply_experience 原生模块
  - 实现 input_defaults 处理器：空 @ 补自我介绍请求；纯可用图片补图片分析提示；普通文本及图文不变。
  - 实现 search_progress 处理器：仅获准执行 search_docs 时请求当前预回复，显式 /search 不新增提示。
  - 将三段默认文案移出 Engine，删除 Engine 对该模块 ID 和 search_docs 预回复业务的硬编码分支。
  - 模块默认启用以保持兼容；整体禁用后，空 @ 返回 empty_input，纯图片仍传图片，文档检索仍执行但不预回复。
  - 验证无 LLM echo 回退、纯表情适配器过滤、图片内容次序、工具调用 ID 和一次提示语义不回退。
  - _Requirement: R1, R2, R4, R6, R9_

- [x] 6. 将模块链纳入 Runtime generation 热替换
  - Runtime Builder 先纯校验模块计划，再准备 ModuleSet 和 Engine；候选未发布时不得发送消息或启动业务后台副作用。
  - 模块配置变化沿用 ActionRestartEngine；Connector、Retriever 继续按现有规则复用，不实现单模块热卸载。
  - 每个请求捕获一个 generation；新请求切换到新链，在途请求及 Gate 已接受工作完成后再 Shutdown Engine 和 ModuleSet。
  - 加入退休 generation 上限：已有一个超期退场实例尚未释放时，新的重建返回明确 runtime_busy，避免连续更新导致无界积累。
  - 覆盖候选构建失败、持久化失败、版本冲突、请求中热更新、Gate flush 并发、退场超时和资源只关闭一次。
  - 不在本任务修复既有反思 publish 的提交后 UpdateConfig；记录为后续有状态模块迁移前审计项。
  - _Requirement: R1, R5, R6, R7, R9_

- [x] 7. 增加状态输出、文档和完整验证
  - GET /v1/admin/runtime 增加稳定排序的模块摘要：ID、来源、启用状态、运行状态、挂载节点/handler_id 和有限错误信息。
  - 日志加入 run_id、generation、module_id、handler_id、hook、duration、outcome，不记录完整提示词、媒体和密钥。
  - 更新 runtime.example.json、配置文档、架构文档和路线图，明确首版覆盖边界、关闭后的行为及未来 FPP 关系。
  - 运行新增包定向测试及 bot/runtime/settings/config/admin 回归，再运行 go test ./...、go vet ./...；环境支持时运行 go test -race ./...，否则明确记录未执行原因。
  - 运行空链/原生链 benchmark，只报告当前变更的调用开销和分配，不据此推断整机或 Prayer 的资源优势。
  - 更新本任务表和 specs/roadmap.md 阶段交接，记录验证、提交/工作区状态、遗留问题及下一阶段建议。
  - _Requirement: R3, R5, R6, R7, R8, R9_

## 执行约束

- 按任务 1→7 顺序推进；每项形成可验证的中间状态，不将 Agent Loop、Session、FPP 或 Web 面板夹带进本阶段。
- 若实施发现设计前提不成立，先更新 requirements/design/tasks 并说明影响，再继续代码。
- 不覆盖工作区中与本计划无关的其他工程师改动。
- 用户于 2026-09-17 已授权本阶段连续实施并要求从当前 master 创建新分支；不含提交、合并或自动进入 Agent Loop 阶段。

## 执行记录

- feat/modular-hooks 基于 a8f63eda4df6ebd381092f8e3e21c40db4c14ac8 创建，保留已有未跟踪计划文件，未覆盖其他分支。
- 配置 envelope/快照隔离先完成；工厂纯校验与公开配置随目录和 Runtime 接入收口，未将模块 ID 硬编码进 config 包。
- ConfigStore 继续使用 settings.Store；JSONFileStore 和 admin 内存 Store 替身均验证更新、历史、回滚与版本冲突。
- 新增 hooks/modules 单测、原生模块组合测试、Engine 路径测试、App 默认注册测试、Runtime 在途切换/失败回滚/退休上限测试。
- Gate 定时回调增加生命周期读锁：保证关闭后的 Wait 不会与新 dispatch/Add 交错，避免模块已释放仍执行旧回调。
- 已有 Gate 待处理项保留关闭时取消/丢弃的语义；等待的是在途直接请求和已启动回调，不宣称把所有缓冲消息处理完或持久化。
- 首版工厂 Build/Close 及原生 Hook 使用协作式取消，不宣称能硬杀任意 Go 代码。
- 普通测试、静态检查通过；竞态测试的二进制以 0xc0000139 启动失败，标准库 errors 的独立空测试也复现。环境：Go 1.25.5 windows/amd64，MinGW gcc 8.1.0。未修改系统工具链，竞态验证未完成。
- 微基准只测当前空链/原生输入链，不是改动前后完整 Bot 对比或 Prayer 资源对比；最终数字记录于阶段交接。

## 最终验证（2026-09-17）

- go test ./...：通过。
- go vet ./...：通过。
- go test ./internal/runtime ./internal/bot ./internal/gate -count=10：通过。
- go test ./internal/hooks ./internal/modules -run '^$' -bench 'Benchmark(EmptyChain|NativeReplyChain)$' -benchmem -count=2：通过。
  - 空输入链：7.838 / 7.908 ns/op，0 B/op，0 allocs/op。
  - 原生回复输入链：1312 / 1283 ns/op，944 B/op，13 allocs/op；包含超时上下文，不启用 debug 日志输出。
- go test -race ./...：无法完成，各测试进程以 0xc0000139 退出；go test -race -run '^$' errors 同样失败，支持该问题独立于 ForgeBot 的判断。尚无可用的竞态检测结论。
- git diff --check、计划相对链接检查、配置示例 JSON 解析：通过。Git 提示 LF/CRLF 自动转换，不是空白检查失败。
- 未连接真实 LLM、QQ 或外部 MCP 做线上验证，未改动部署配置或运行中的服务。

## 下一阶段建议

本阶段完成的是内部模块挂载首版，不是完整 Plugin 系统。下一阶段按全局路线进入有界 Agent Loop 的需求/设计，先复核工具预算、取消、终止条件与本次节点的适配；不自动恢复向量检索或引入聊天记录持久化。重新运行竞态测试仍为待办环境验证项。
