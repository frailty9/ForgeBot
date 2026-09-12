# Implementation Plan

- [x] 1. 建立 Search 配置与协议中立契约
  - 在运行时配置中增加 `SearchConfig`、默认值、严格校验和 duration JSON 编解码。
  - 定义 `Retriever`、`Query`、`Response`、`Result`、`Status`、`ReloadResult` 和稳定错误类型。
  - 更新运行时配置示例，默认关闭 Search，启用时只接受 `on_demand/structured`。
  - 覆盖配置默认值、非法后端、预算边界、JSON 严格字段和历史兼容测试。
  - _Requirement: 13, 17, 19, 20_

- [x] 2. 实现安全的 MD/MDX 解析与分段
  - 引入 Markdown AST 解析器，提取 frontmatter、标题层级、正文、列表、引用和 fenced code block。
  - 对 MDX import/export、JSX 与表达式执行容错文本提取，不执行 JavaScript。
  - 实现稳定 section ID、块边界拆分、相对路径规范化和 rune 字符预算。
  - 拒绝路径越界、符号链接/reparse point、非普通文件、非法扩展名与超限文件。
  - 覆盖 Markdown、MDX、Windows 路径、局部损坏、稳定 ID 和资源限制测试。
  - _Requirement: 9, 10, 11, 12_

- [x] 3. 实现内存结构化索引和确定性排序
  - 实现 Unicode NFKC、英文/标识符拆词、路径词项与 CJK unigram/bigram tokenizer。
  - 构建不可变倒排索引及字段长度统计。
  - 实现 BM25F 字段权重、短语加分、覆盖率加分和最低命中覆盖率过滤。
  - 生成有界 snippet、来源元数据并按稳定次序解决同分结果。
  - 覆盖中英文、标题、标签、路径、代码、弱相关过滤和重复查询稳定性测试。
  - _Requirement: 8, 9, 11, 12_

- [x] 4. 实现可原子重载的 Structured Retriever
  - 实现目录遍历、文件数/总字节/build timeout 限制及 warning 汇总。
  - 使用不可变 Index 快照和原子指针，使查询不被索引构建长时间阻塞。
  - 实现 reload 单飞、失败保留旧索引、成功统计与安全状态输出。
  - 区分致命构建错误、单文件 warning、有效空索引和查询超时。
  - 覆盖首次构建、空库、部分 warning、原子替换、并发查询和失败保留测试。
  - _Requirement: 7, 8, 12, 15, 16, 17_

- [x] 5. 补全三种 LLM 协议的工具往返语义
  - 扩展统一消息模型，提供 assistant tool call 与 tool result 构造器。
  - 为 OpenAI Chat Completions 序列化 `tool_calls` 和 `role=tool` 消息。
  - 为 OpenAI Responses 序列化 `function_call` 与 `function_call_output` item。
  - 为 Anthropic Messages 序列化 `tool_use` 与 `tool_result` content block。
  - 保持协议细节封装于 LLM 客户端，并增加三个协议的请求快照测试。
  - _Requirement: 5, 6, 7, 18_

- [x] 6. 建立受限 Tool Executor 与 Search 上下文构建器
  - 定义工具 Definition、Call、Result 和 Executor 契约，实现显式白名单 Registry。
  - 实现只读 `search_docs` 工具，严格解析 `query` 和 `limit`，施加查询次数与超时预算。
  - 对未知工具、非法参数、超时、未命中和预算耗尽返回稳定错误码。
  - 实现带来源边界、提示注入警告和总字符上限的检索上下文构建器。
  - 确保工具执行器不具备 Shell、网络、SecretStore、配置写入或平台发送能力。
  - _Requirement: 5, 6, 7, 8, 18, 20_

- [x] 7. 在 Bot 中接入按需检索和显式 `/search`
  - 普通消息首次 LLM 请求只携带工具定义，不读取索引或注入文档。
  - 实现有绝对轮次上限的非流式工具循环，默认最多一次检索和两次 LLM 请求。
  - 实现 `/search <query>` 快捷路径、空参数提示、未命中回复及无 LLM 时的来源摘要降级。
  - 调整 operations Router，使其只截获已识别的管理命令，其他命令继续传给 Bot。
  - 覆盖无检索直接回答、合法调用、失败降级、预算终止及管理群 `/search` 测试。
  - _Requirement: 1, 2, 3, 4, 5, 6, 7, 19_

- [x] 8. 接入 Runtime generation 与配置热切换
  - 为 Runtime Builder 增加 Retriever factory，并由 generation 持有 Retriever 与 Tool Executor。
  - Search 配置变化时预构建候选索引；构建失败时不持久化、不发布新 generation。
  - 非 Search 配置变化和显式 runtime restart 复用现有 Retriever，避免重复扫描文档。
  - 增加 `ActionReplaceSearch`，并将实际组件复用逻辑从单一 Action 分类中分离。
  - 覆盖 Search 单独变化、多配置同时变化、失败回滚、禁用及配置历史回滚测试。
  - _Requirement: 13, 14, 17, 19, 20_

- [x] 9. 接通 `/reload-docs`、管理 API、状态与通知
  - 实现 `runtime.Manager.ReloadSearch`，协调配置提交与 reload 竞态且不改变配置版本。
  - 将 `/reload-docs` 从保留命令接到异步 reload，并返回文档数、section 数、耗时和时间。
  - 增加受认证的 `POST /v1/admin/search/reload`，并在 runtime 状态及 `/status` 中展示 Search 摘要。
  - 发布成功/失败 Search 事件；通知时跳过已收到命令直接回复的来源群，避免重复消息。
  - 覆盖 reload 单飞、配置竞态、旧索引保留、管理接口认证、状态和通知去重测试。
  - _Requirement: 15, 16, 17, 19_

- [x] 10. 完成端到端验证与项目文档
  - 使用临时 MD/MDX 文档库验证普通问答零检索、显式搜索和 LLM 工具检索完整路径。
  - 验证 Search 配置更新、失败保活、回滚、文档重载及多个管理群通知行为。
  - 更新 README、架构文档、路线图和运行时配置说明。
  - 明确当前仅支持结构化后端，向量与混合检索继续挂起。
  - 运行 `go test ./...`、`go vet ./...` 和 `git diff --check`。
  - _Requirement: 1-20_

## 建议提交顺序

1. `feat(search): 建立检索配置与核心契约`
2. `feat(search): 实现MDX解析与结构化索引`
3. `feat(llm): 补全工具调用往返协议`
4. `feat(bot): 接入按需文档检索`
5. `feat(runtime): 支持检索热切换与重载`
6. `docs(search): 完善结构化检索文档`

每次提交前执行该阶段的聚焦测试；最后一次提交前执行完整测试和静态检查。若一个任务同时触及多个提交主题，应以可编译、可测试为边界拆分，不提交不可运行的中间状态。
