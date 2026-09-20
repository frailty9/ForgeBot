# Connector Provider 模块化技术设计

日期：2026-09-20。

状态：设计已于 2026-09-20 确认；当前进入任务拆分，任务确认前不修改生产代码。

## 1. 设计结论

本阶段在现有模块运行时中增加一种宿主扩展面：`connector_providers`。内置
`native.echo` 与 `native.onebot` 作为普通原生模块注册 Provider；连接器实例仍由
`platforms.connectors[]` 声明，通过 `type` 选择 Provider；群组仍由
`platforms.groups[]` 完成协议无关绑定。

目标依赖关系如下：

```text
app composition root
  └─ 注册原生模块 Factory
       ├─ native.echo    ─┐
       └─ native.onebot ─┴─> modules.Set.ProviderCatalog
                                  │
runtime.json                      │ type lookup
  ├─ modules.native.*.options ────┤
  ├─ platforms.connectors[] ──────┤
  └─ platforms.groups[] ──────────┘
                                  │
                                  v
                         platform.Adapter[]
                                  │
                                  v
                         platform.ConnectorSet
                                  │
                                  v
                         Bot Engine / Router
```

Core 只知道 Provider 契约和连接器公共字段。`onebot.Config`、认证、Webhook、
消息段、引用和媒体处理继续留在 OneBot 实现及其原生模块中。

## 2. 配置模型

### 2.1 通用连接器配置

`internal/config` 仅保留通用结构：

```go
type ConnectorConfig struct {
    ID           string          `json:"id"`
    Type         string          `json:"type"`
    Enabled      bool            `json:"enabled"`
    PromptSuffix string          `json:"prompt_suffix,omitempty"`
    Options      json.RawMessage `json:"options,omitempty"`
}
```

删除核心中的 `OneBotConnectorConfig`、其专用 JSON 编解码和
`ConnectorConfig.OneBot`。`Options` 在 clone 时复制字节，空值规范化为 `{}`，避免
generation 之间共享可变底层数组。

Core 只校验：

- `id`、`type` 非空且格式合法；
- 连接器 ID 唯一；
- 群组 ID 唯一并引用已声明的连接器；
- `target` 非空；
- 单个逻辑会话只能绑定一次；
- 数量和文本长度上限。

“OneBot 至少绑定一个群组”等协议约束迁入 OneBot Provider。Core 不再按类型做
条件校验。

### 2.2 模块配置与实例配置

模块级 options 表示协议默认值，实例 options 表示具体端点配置。Core 同时透传
两份原始 JSON，Provider 负责解析、默认值和覆盖：

```text
模块实现内置默认值
  < native.onebot.options
  < platforms.connectors[qq-main].options
```

OneBot 首期模块级 options 采用与实例 options 同名的可选默认字段。安全字段与
敏感字段都可被 Provider 正确处理，但文档建议 Token/Secret 留在实例层，避免多个
账号意外共享凭据。

这是未发布项目的破坏式变更，不读取旧的 `connectors[].onebot`。

## 3. Provider 契约

### 3.1 平台层契约

在 `internal/platform` 增加 Provider 契约，使模块系统依赖抽象而非 OneBot：

```go
type ConnectorProvider interface {
    Type() string
    ValidateOptions(moduleOptions, connectorOptions json.RawMessage,
        bindings []ConnectorBinding) error
    PublicOptions(moduleOptions, connectorOptions json.RawMessage) (any, error)
    Build(context.Context, ConnectorBuildRequest) (Adapter, error)
}

type ConnectorBuildRequest struct {
    ConnectorID      string
    PromptSuffix     string
    ModuleOptions    json.RawMessage
    ConnectorOptions json.RawMessage
    Bindings         []ConnectorBinding
    Logger           *slog.Logger
}

type ConnectorBinding struct {
    ConversationID string
    Target         string
    PromptSuffix   string
}
```

`ConnectorBinding` 是已经按 connector ID 筛选的 Provider 输入，不携带整个
`PlatformConfig`。现有用于出站路由的 `platform.ConversationBinding` 保持精简，
Core 在创建 `ConnectorSet` 时另行生成。

Provider 约束：

- `Type()` 返回规范化、稳定的类型名；
- `ValidateOptions` 不产生外部副作用；
- `PublicOptions` 必须返回脱敏后的可序列化值；
- `Build` 不监听端口、不启动 goroutine、不发送网络请求；外部行为只允许在
  `Adapter.Start` 后发生；
- Provider 不得持有 Runtime Manager、全局 RuntimeConfig、ConfigStore 或
  SecretStore；
- Adapter 的生命周期仍归 `platform.ConnectorSet` 所有。

### 3.2 模块注册

模块运行时增加：

```go
const RegistrationConnectorProviders RegistrationKind = "connector_providers"

type ConnectorProviderRegistration struct {
    Provider      platform.ConnectorProvider
    ModuleOptions json.RawMessage
}

type ExtendedRegistrations struct {
    // existing registrations...
    ConnectorProviders []ConnectorProviderRegistration
}
```

`Descriptor.Registrations` 必须声明 `RegistrationConnectorProviders`，实例才可返回
Provider。模块 Registry 在构建 `modules.Set` 时：

1. 校验返回类型已声明；
2. 校验 Provider 非空、类型名合法；
3. 复制模块 options；
4. 按类型建立不可变目录；
5. 若两个已启用模块注册同一类型，构建失败并同时报告模块 ID。

`modules.Set` 对宿主提供只读方法：

```go
ConnectorProvider(typeName string) (ProviderRegistration, bool)
ConnectorProviderTypes() []string
```

Provider 不是 capability。Capability 用于模块间的强类型依赖；Connector Provider
是宿主拥有、由模块实现的专用注册面，和 Tools、Commands 的定位一致。

## 4. 原生协议模块

### 4.1 `native.echo`

新增原生 Echo 模块：

- 模块 ID：`native.echo`；
- Provider type：`echo`；
- 默认启用，以保持默认配置可直接启动；
- options 首期为空对象，出现未知字段时返回明确错误；
- Build 调用现有内存 Adapter 构造函数；
- 无连接器实例时模块仍能正常处于 active 状态。

Echo 的作用不仅是开发测试，也是验证“新增协议无需修改 Core 分支”的最小实现。

### 4.2 `native.onebot`

新增原生 OneBot 模块：

- 模块 ID：`native.onebot`；
- Provider type：`onebot`；
- 默认关闭，部署者显式启用后才可使用 OneBot 实例；
- 模块 Factory 校验模块默认 options；
- Provider 合并模块与实例 options 后生成现有 `onebot.Config`；
- Provider 把 `ConnectorBinding` 转换为 `map[target]onebot.Group`；
- OneBot 私有校验、默认时长和限制值移出 `internal/config`；
- `send_token`、`receive_secret` 在公开视图中仅输出 `*_configured`；
- 现有 Adapter、消息解析、媒体解析和 HTTP 行为尽量不改，只移动构造边界。

若 OneBot 实例未绑定任何群组，Provider 拒绝构建。此规则不再出现在 Core。

## 5. Generation 构建顺序

当前顺序先构建 Connector，再构建模块，无法使用模块提供的 Provider。新顺序为：

```text
1. clone + 通用配置校验
2. 模块配置与依赖校验
3. 构建 LLM client 和宿主初始 capabilities
4. 构建 modules.Set，得到 Provider Catalog
5. 校验所有 enabled connectors 的 Provider 与私有 options
6. 由 Provider 构建 Adapter，并组装 ConnectorSet
7. 用 ConnectorSet 作为 Sender 构建 Engine
8. 构建 operations router / administration 等 generation 组件
9. publish 时才 Start ConnectorSet 和模块后台任务
```

若第 5 至第 8 步失败，候选 `modules.Set` 和已构建 Engine 必须被关闭。由于 Provider
Build 不允许启动外部资源，失败候选不会占用 Webhook 端口。`ConnectorSet.Start`
仍保留现有的部分启动回滚行为。

`runtime.Builder.ConnectorFactory` 与 `app.connectorFactory` 被删除，替换为 Runtime
内部的通用 `buildConnectorSet(moduleSet, platformConfig, logger)`。这个函数只做：

- 查 Provider；
- 按 connector ID 筛选 binding；
- 调用 Provider；
- 生成通用出站绑定；
- 调用 `platform.NewSet`。

它不导入 `platform/onebot` 或 `platform/memory`。

## 6. 配置验证与公开视图

### 6.1 验证分层

配置验证分三层：

1. `config.RuntimeConfig.Validate`：只做结构、引用和通用上限校验；
2. `modules.Registry.Validate`：模块存在性、模块 options、声明依赖校验；
3. Provider Catalog：enabled connector 的类型存在性与私有 options 校验。

Runtime apply 必须执行三层并完整构建候选 generation。管理 API 的 validate 操作也
必须执行 Provider 校验，但不启动 Adapter。为避免构建检索索引等无关模块，
Registry 增加只准备声明了 `connector_providers` 的模块目录的方法；它使用相同 Factory
和注册校验，使用后立即关闭临时实例。

允许的状态：

```text
Provider enabled + 0 connector instances       -> valid
unknown Provider + disabled connector instance -> valid but unavailable
unknown Provider + enabled connector instance  -> invalid
duplicate enabled Provider type                 -> invalid
```

disabled 且 Provider 不可用的实例不会公开原始 options；管理视图只显示公共字段和
`provider_available: false`，避免 Core 在无法识别字段时泄漏凭据。

### 6.2 安全的公开配置

`Snapshot.Public` 不再直接序列化连接器私有 options。它只产生公共连接器字段，且
默认省略 options。Runtime Manager 增加 Provider 感知的公开渲染：

```go
PublicPlatforms(config.RuntimeConfig) (map[string]any, error)
```

对可用 Provider，它调用 `PublicOptions` 并把结果放在 `options`；对不可用且 disabled
的 Provider，只返回 `provider_available: false`。所有管理 API 的读取、应用结果和
状态响应必须经过同一安全渲染入口，不能继续直接调用 `PlatformConfig.Public()`
输出协议数据。

模块自身的 `PublicOptions` 同样负责 `native.onebot.options` 的脱敏。这样模块默认
配置和连接器实例配置都不会泄漏 Token/Secret。

## 7. 热更新分类与发布

目前 `classify` 只看到 `Platforms` 和整个 `Modules`。新分类规则按以下优先级执行：

```text
platforms changed
  OR 声明 connector_providers 的模块 enabled/options changed
    -> restart_connectors

其他 modules changed
    -> replace_modules

仅 LLM changed
    -> replace_llm
```

模块 Registry 根据 Descriptor 的注册种类判断哪些模块会影响连接器，Runtime 不按
`native.onebot` 等 ID 写死判断。若一次更新同时改变 Provider 模块和其他模块，完整
候选 generation 仍统一构建，外部动作按 `restart_connectors` 执行。

发布沿用当前事务语义：

1. 停止旧 ConnectorSet；
2. 启动新 ConnectorSet；
3. 持久化配置；
4. 发布 generation 并启动模块；
5. 失败时停止新集合并恢复旧集合。

旧 ConnectorSet 在对应旧模块实例退休前停止，避免 Adapter 依赖的 Provider 代际
状态提前销毁。Provider 本身应保持无运行期可变状态，实际运行状态归 Adapter。

## 8. 包组织与依赖方向

建议新增：

```text
internal/platform/provider.go
  Provider 契约、BuildRequest、ConnectorBinding

internal/modules/echo/module.go
  native.echo Factory / Instance / Provider

internal/modules/onebot/module.go
  native.onebot Factory / Instance / Provider / options merge
```

保留：

```text
internal/platform/memory
  Echo Adapter 实现

internal/platform/onebot
  OneBot Adapter 与协议行为
```

依赖方向：

```text
app -> modules/echo, modules/onebot, modules.Registry
modules/echo -> modules + platform + platform/memory
modules/onebot -> modules + platform + platform/onebot
runtime -> modules + platform + config
config -X-> platform/onebot
app    -X-> platform/onebot, platform/memory（构造用途）
```

组合根仍显式注册所有原生模块，这是发行版包含哪些原生能力的唯一代码清单；但它
不再负责构造任何协议 Adapter。

## 9. 错误、日志与状态

错误应带有稳定定位上下文：

```text
platforms.connectors[qq-main]: provider "onebot" is unavailable
platforms.connectors[qq-main].options: webhook_path must start with /
connector provider type "onebot" registered by both native.onebot and ...
```

日志和错误不得包含原始 Token、Secret 或完整私有 options。Connector 状态仍由
`platform.ConnectorSet.Status()` 提供；模块状态中 `native.onebot` 可显示所提供的
type，但不复制每个连接器的运行状态，避免两套状态源。

## 10. 测试策略

### 10.1 单元测试

- 模块 Registry：注册种类声明、Provider 类型冲突、禁用模块不注册 Provider；
- 通用配置：RawMessage clone、公共字段、群组引用，不含协议条件分支；
- Echo Provider：空配置、未知字段、Adapter ID 与绑定；
- OneBot Provider：默认值、实例覆盖、必填项、群组规则、公开视图脱敏；
- Provider Catalog：缺失 Provider、disabled 未知实例、错误路径包装；
- 热更新分类：平台变化、Provider 模块变化、普通模块变化及组合变化。

### 10.2 集成与回归测试

- Runtime 初始 generation 使用 Provider 构建 Echo/OneBot；
- 候选构建不启动 Webhook；
- OneBot 端口冲突时新集合启动失败并恢复旧集合；
- 持久化失败时恢复旧连接器；
- 管理配置读取和 apply 响应不回显密钥；
- OneBot 现有 webhook、引用、图片、空 @、出站回复测试继续通过；
- `go test ./...`、`go vet ./...`。

## 11. 迁移与提交边界

实施阶段建议保持可二分历史：

1. 建立 Provider 契约及模块注册目录，不迁移具体协议；
2. 将 Echo 迁为 `native.echo`，证明 Core 通用构建链；
3. 将 OneBot 配置与构造迁为 `native.onebot`；
4. 收口公开视图、验证和热更新分类；
5. 更新示例、配置文档与架构文档，完成回归审计。

中间提交必须保持测试可编译；若需要短暂适配层，只能存在于同一提交内部，不把
同时运行的新旧 OneBot 配置模型留在可见历史节点。
