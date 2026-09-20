# Connector Provider 模块化实施计划

依据：[requirements.md](requirements.md) 与 [design.md](design.md)。

状态：任务已于 2026-09-20 确认；第一阶段已完成，进入 Echo 迁移。

## 1. 建立 Provider 契约与模块注册目录

- [x] 1.1 在 `internal/platform` 定义 `ConnectorProvider`、
      `ConnectorBuildRequest` 和筛选后的 `ConnectorBinding`，明确 Build 无外部副作用、
      Adapter 才拥有运行期生命周期。
- [x] 1.2 为模块运行时新增 `connector_providers` 注册种类与注册结构，并在
      Descriptor/ExtendedRegistrations 校验中拒绝未声明注册、nil Provider 和非法类型。
- [x] 1.3 在 `modules.Set` 建立按 type 查询的不可变 Provider Catalog，保留提供者模块
      ID 和模块 options，拒绝两个已启用模块注册相同 type。
- [x] 1.4 提供 Registry 查询“哪些模块影响连接器”的通用元数据能力，供后续热更新
      分类使用，禁止 Runtime 写死 `native.onebot` 等模块 ID。
- [x] 1.5 增加 Provider 注册、禁用模块、重复 type、模块 options 隔离和目录只读行为测试。
- Requirements: 5.1, 5.3；Design: 3.1, 3.2

完成标准：新增抽象不改变现有 Echo/OneBot 构建路径；`go test ./internal/modules/...`
和 `go test ./internal/platform/...` 通过。

建议提交：`feat: 建立连接器Provider注册模型`

## 2. 迁移 Echo 并建立通用连接器构建链

- [ ] 2.1 新增 `native.echo` 模块和 `echo` Provider；模块默认启用，严格接受空 options，
      Provider 构建现有 memory Adapter。
- [ ] 2.2 在应用组合根注册 `native.echo`，使发行版原生能力仍由组合根显式决定。
- [ ] 2.3 在 Runtime 中实现通用 ConnectorSet 构建器：查找 Provider、按 connector ID
      筛选群组绑定、构建 Adapter、生成出站绑定并调用 `platform.NewSet`。
- [ ] 2.4 调整 generation 准备顺序为 Client/Capabilities → Module Set → Connector Set
      → Engine/Router，确保候选构建失败时逆序关闭已准备资源且不启动 Adapter。
- [ ] 2.5 将 Echo 从 `app.connectorFactory` 类型分支迁出；暂时保留 OneBot 的窄适配
      Provider，使本提交结束时所有 connector 都经由同一通用构建链且现有配置可运行。
- [ ] 2.6 增加 Echo Provider、绑定筛选、Provider 缺失、候选构建不启动 Adapter、
      构建失败资源清理和默认 Snapshot 启动测试。
- Requirements: 4.1, 4.2, 5.1, 5.3；Design: 4.1, 5

完成标准：应用和 Runtime 不再按 `echo` 构造 Adapter；默认 Echo 配置行为不变；
`go test ./internal/app/... ./internal/runtime/... ./internal/modules/echo/...` 通过。

建议提交：`refactor: 将Echo迁入原生协议模块`

## 3. 将 OneBot 配置与构造迁入原生协议模块

- [ ] 3.1 将通用 `ConnectorConfig` 收口为 `id/type/enabled/prompt_suffix/options`，
      实现 RawMessage 深拷贝和空对象规范化；删除 Core 的 `OneBotConnectorConfig`、
      专用 duration 编解码及 `onebot` 嵌套字段。
- [ ] 3.2 将 PlatformConfig 校验缩减为公共字段、数量、唯一性和绑定引用；移除
      `type == onebot`、OneBot 必须绑定群组等协议条件分支。
- [ ] 3.3 新增 `native.onebot` Factory/Instance/Provider，严格解析模块默认 options 与
      实例 options，定义字段级覆盖规则和默认时长/限制值。
- [ ] 3.4 将 OneBot 私有校验、至少一个群组规则、绑定到 `onebot.Group` 的转换和
      `onebot.Config` 构造迁入 Provider，保持现有 Adapter 协议实现不变。
- [ ] 3.5 在组合根注册 `native.onebot`，删除临时 OneBot 适配 Provider、
      `app.connectorFactory` 以及 app/runtime 对 `platform/onebot` 的构造依赖。
- [ ] 3.6 破坏式更新所有测试夹具和默认配置：Echo 使用 `options: {}`，OneBot 使用
      `connectors[].options` 且必须显式启用 `native.onebot`；不保留旧字段兼容层。
- [ ] 3.7 回归 OneBot 多实例、群组映射、实例覆盖模块默认值、必填项错误路径、Webhook
      构建不抢端口，以及现有认证、重放、引用、图片、空 @ 和出站回复测试。
- Requirements: 3.1, 3.2, 5.2, 5.3, 5.4；Design: 2, 4.2, 5

完成标准：Core 和 app 中不存在 OneBot 私有配置或按类型构造分支；旧
`connectors[].onebot` 被严格拒绝；所有 OneBot 回归测试通过。

建议提交：`refactor: 将OneBot迁入原生协议模块`

## 4. 收口 Provider 验证与安全公开视图

- [ ] 4.1 实现 Provider Catalog 对 enabled connector 的依赖和私有 options 校验：
      缺失 Provider 时报告 connector ID/type，disabled 未知实例保持合法。
- [ ] 4.2 为管理 API 的纯验证路径准备仅包含 Provider 的临时目录，复用模块 Factory
      与注册校验并立即关闭实例，不构建无关检索索引或启动后台任务。
- [ ] 4.3 实现 Provider 感知的连接器公开视图；可用 Provider 负责渲染 options，
      disabled 且 Provider 不可用的实例仅显示公共字段和 `provider_available: false`。
- [ ] 4.4 OneBot Provider 对实例 options 输出 `send_token_configured`、
      `receive_secret_configured`，不返回原始凭据；模块 Factory 对模块默认 options
      使用相同脱敏原则。
- [ ] 4.5 将 admin 读取、validate/apply 结果及相关 server 状态响应切换到统一安全渲染
      入口，禁止任何路径直接序列化 connector RawMessage。
- [ ] 4.6 增加未知 Provider、disabled 未知实例、重复 Provider、错误定位和管理 API
      密钥不回显测试，并扫描日志/错误中是否出现完整私有 options。
- Requirements: 5.1, 5.2；Design: 6, 9

完成标准：所有管理读取和变更响应都经过 Provider 脱敏；Core 不猜测敏感字段名；
安全回归测试通过。

建议提交：`refactor: 收口连接器配置验证与公开视图`

## 5. 完成 Provider 感知的热更新与生命周期事务

- [ ] 5.1 扩展变更分类：Platforms 变化或声明 `connector_providers` 的模块 enabled/options
      变化均返回 `restart_connectors`；普通模块变化仍为 `replace_modules`。
- [ ] 5.2 调整重用旧 ConnectorSet 的条件，保证 Provider 配置发生变化时必建新集合，
      普通 Bot/LLM/业务模块更新仍可安全复用连接器。
- [ ] 5.3 审计发布顺序，保证旧 ConnectorSet 在旧 Provider 模块退场前停止，新集合仅在
      publish 阶段启动，持久化或启动失败时恢复旧集合。
- [ ] 5.4 覆盖 OneBot 模块启停、模块默认 options 更新、实例 options 更新、群组绑定更新、
      普通模块更新和混合更新的 action 分类与 generation 代际测试。
- [ ] 5.5 增加新 ConnectorSet 启动失败、持久化失败、旧集合恢复失败和部分 Adapter
      启动回滚测试；确保状态与通知包含 connector ID 但不泄密。
- Requirements: 5.1, 5.3, 5.4；Design: 5, 7, 9

完成标准：所有 Provider 相关更新均按连接器重启事务发布；失败后旧 generation
仍可服务；Runtime 生命周期测试通过。

建议提交：`refactor: 完善连接器Provider热更新事务`

## 6. 更新配置示例、文档与迁移说明

- [ ] 6.1 更新 `configs/runtime.example.json` 和测试/本地示例，加入 `native.echo`、
      `native.onebot`，将 OneBot 实例字段迁入 `options`。
- [ ] 6.2 更新 README 与配置文档，说明协议模块、连接器实例、群组绑定三层关系，
      提供单实例、多实例、模块默认值和实例覆盖示例。
- [ ] 6.3 更新架构文档，记录 Provider 注册链、generation 构建顺序、生命周期所有权、
      管理视图脱敏责任和新增协议的最小实现步骤。
- [ ] 6.4 明确破坏式迁移：删除 `connectors[].onebot`，启用 OneBot 实例前必须启用
      `modules.native.onebot`；列出旧字段到新字段的映射。
- [ ] 6.5 检查文档和示例中不存在旧 OneBot 配置结构、Core 类型 switch 或将实例放入
      `modules.native.onebot.options` 的错误表述。
- Requirements: 3, 4, 6；Design: 2, 8, 11

完成标准：使用文档可独立指导现有 OneBot 用户完成配置迁移和多实例配置。

建议提交：`docs: 更新连接器Provider配置与架构说明`

## 7. 收尾审计与全量验证

- [ ] 7.1 使用 `rg` 审计 `OneBotConnectorConfig`、`ConnectorConfig.OneBot`、
      `switch connector.Type`、旧 `connectors[].onebot` 和直接序列化 options 的残留。
- [ ] 7.2 运行 Provider/模块/Runtime/OneBot 定向测试，再运行 `go test ./...` 与
      `go vet ./...`；如执行 race 测试，单独记录 Windows 二进制加载限制。
- [ ] 7.3 审核每个提交均可编译、测试且职责单一；不得把格式化噪声或无关重构混入。
- [ ] 7.4 更新本任务清单状态，记录实际提交、配置迁移影响、验证结果与剩余风险。
- Requirements: 5, 7；Design: 10, 11

完成标准：工作树干净，全量测试和 vet 通过，规格与代码一致，分支可供人工 OneBot
烟雾测试及合并审查。

建议提交：`docs: 完成连接器Provider模块化任务清单`

## 计划提交序列

```text
feat: 建立连接器Provider注册模型
refactor: 将Echo迁入原生协议模块
refactor: 将OneBot迁入原生协议模块
refactor: 收口连接器配置验证与公开视图
refactor: 完善连接器Provider热更新事务
docs: 更新连接器Provider配置与架构说明
docs: 完成连接器Provider模块化任务清单
```

实施中如某一阶段同时包含无法分离的配置破坏与调用方迁移，可在该阶段内部调整
子任务顺序，但不得改变阶段验收边界，也不得留下需要下一提交才能恢复编译或运行的
中间提交。
