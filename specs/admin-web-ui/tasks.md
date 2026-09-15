# Admin Web UI 实施计划

- [x] 1. 建立 Vapor 工程基线与兼容性门禁
  - 创建 `web/` 下的 Vue 3.6 Vapor、TypeScript、Vite、Pinia、SCSS 和 UnoCSS 工程。
  - 锁定 RC 依赖版本，验证纯 Vapor 入口、Pinia、SFC、SCSS 与生产构建。
  - 增加 typecheck、lint、unit、build 和 bundle-size 脚本。
  - 不引入传统 VDOM 组件库；若依赖无法通过纯 Vapor 验证则替换依赖。
  - _Requirement: 15_

- [x] 2. 补齐可安全编辑的管理 API 契约
  - 定义稳定的 Admin DTO 和错误响应，避免前端依赖 Go 内部结构或解析错误字符串。
  - 让脱敏配置支持安全往返，未提交的敏感字段保持原值。
  - 为 OneBot/MCP 等敏感配置提供明确的 keep/replace/delete 语义。
  - 保持 LLM API Key 独立只写接口，并补齐契约测试。
  - _Requirement: 5-10, 13_

- [x] 3. 实现应用壳、认证和强类型请求层
  - 实现 Vapor 应用入口、工业化布局、轻量 History 路由和错误边界。
  - 实现 `sessionStorage` Bearer 会话、登录验证、401 清理和退出。
  - 实现强类型 `fetch` client、DTO 收窄、取消请求和统一错误模型。
  - 建立 CSS tokens、SCSS 模块和基础可访问组件。
  - _Requirement: 1-3, 13-15_

- [x] 4. 实现概览与运行维护页面
  - 实现 runtime 信号轨、版本/LLM/连接器/检索/反思/Gate 状态摘要。
  - 实现软重启、索引重载和反思扫描，并防止重复提交。
  - 展示操作结果、禁用原因和服务端错误。
  - _Requirement: 4, 11, 14_

- [ ] 5. 完成结构化配置编辑与密钥维护
  - [x] 已完成 Bot、LLM、Gate 首版编辑、API Key 只写操作和安全 JSON 预览。
  - [ ] 仍需补齐 Search、Platforms、Administration、Reflection、Operations、MCP 和 Skills 的领域表单。
  - 建立基准快照、配置草稿、dirty paths 和服务端校验状态。
  - 按领域实现 Bot、LLM、Gate、Search、Platforms、Administration、Reflection、Operations、MCP 和 Skills 表单。
  - 实现动态列表引用提示、静态配置只读区和预计应用影响。
  - 实现 API Key 独立替换/删除，不将正文写入持久状态。
  - 实现版本冲突时保留草稿并重新加载。
  - _Requirement: 5-10, 13, 14_

- [x] 6. 实现版本历史与回滚
  - 实现版本时间轴、当前版本定位和历史加载状态。
  - 实现带 `expected_version` 的回滚确认和冲突处理。
  - 回滚后刷新配置、runtime 与历史状态。
  - _Requirement: 12-14_

- [x] 7. 将前端嵌入 Go 服务
  - 构建资源至 Go 嵌入目录并通过 `/admin/` 提供。
  - 实现 SPA fallback、缓存头和安全响应头，确保 `/v1/*` 不被 fallback 捕获。
  - 提供开发代理和发布构建命令，正式运行不依赖 Node.js。
  - _Requirement: 15_

- [ ] 8. 完成自动化验证与使用文档
  - [x] 已完成 API client、配置草稿、会话、应用壳和 embed 单元测试。
  - [ ] 仍需补齐 Playwright 关键路径和真实浏览器回归验证。
  - 补齐前端单元测试和 Playwright 关键路径测试。
  - 运行 typecheck、lint、unit、build、bundle-size、`go test ./...` 和 `go vet ./...`。
  - 在浏览器验证登录、概览、配置保存、冲突、密钥、运维与回滚流程。
  - 更新 README、配置文档、架构文档和路线图。
  - _Requirement: 1-15_
