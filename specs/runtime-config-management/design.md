# 应用层配置热切换设计

## 1. 总体架构

```text
启动 YAML + 环境变量覆盖 ─┐
                          ├─> config.Loader ──> Snapshot
JSON 热更新文件 ──────────┘                    ↓
                                     settings.Store
                                          ↓
管理 API ─────────────────────────> runtime.Manager
                                          ├─ in-place components
                                          ├─ llm.Client (atomic replacement)
                                          ├─ bot.Engine
                                          ├─ platform adapters
                                          └─ search/tools (future)

LLM API Key ──只写管理 API──> SecretStore ──引用/版本──> runtime.Manager
```

配置加载与持久化职责分开：

- `config.Loader` 读取启动 YAML、环境变量覆盖和 JSON 热更新文件，并执行默认值和校验；
- `settings.Store` 只负责运行时快照、版本、历史和 JSON 热更新文件持久化；
- `runtime.Manager` 只负责把快照转换为运行中的组件。

三者都不向具体平台暴露全局配置读取能力。静态配置源是启动边界，不能由 `settings.Store` 覆盖。

## 2. 配置模型

### 2.1 启动配置

`config.StaticConfig` 保存进程级边界，例如 HTTP 地址、热更新 JSON 路径、SecretStore 路径、认证和基础设施参数。YAML 是正式介质，环境变量是部署覆盖层，其来源优先级为：

```text
环境变量 > 静态 YAML > 编译/代码默认值
```

启动 YAML 只在启动时读取。环境变量只覆盖明确列出的启动字段，不覆盖 JSON 热更新字段；ForgeBot 本身不读取 `.env` 文件。

### 2.2 运行时配置

`config.RuntimeConfig` 保存可由管理面板修改的应用行为，例如 Log、Bot、Gate、LLM、Platforms，以及未来 Search、Tools。其来源为 JSON 热更新文件，管理 API 通过原子替换写回该文件。`LLMConfig` 只保存 `APIKeyRef`，禁止出现 API Key 正文。

`config.Config` 可以暂时作为对外兼容的组合结构，但组合根必须明确把它拆为：

```go
type Snapshot struct {
    Version   uint64
    UpdatedAt time.Time
    Static    StaticConfig
    Runtime   RuntimeConfig
}
```

快照在发布后视为不可变值；更新通过复制、修改、校验、发布完成，禁止原地修改已发布对象。

### 2.3 LLM 密钥

LLM API Key 经独立的只写管理接口进入 `SecretStore`。普通配置读取、历史和 runtime 状态只暴露 `configured`、更新时间和版本，不返回正文或尾号。首版 SecretStore 使用权限受限的独立文件，并保持接口可替换；文件格式和主密钥方案在实现该任务时固定。

运行时创建 LLM client 时，由 `runtime.Manager` 使用 `APIKeyRef` 从 SecretStore 获取密钥并以参数注入；`config.RuntimeConfig`、`bot.Engine` 和 LLM 协议客户端均不负责读取 SecretStore。

## 3. Store 接口

建议接口：

```go
type Store interface {
    Current(context.Context) (Snapshot, error)
    Update(context.Context, UpdateRequest) (Snapshot, error)
    Rollback(context.Context, uint64, uint64) (Snapshot, error)
    History(context.Context, int) ([]SnapshotMeta, error)
}
```

`UpdateRequest` 必须包含 `ExpectedVersion`、变更后的运行时配置、变更来源和是否允许覆盖。默认使用乐观并发控制；版本不匹配直接返回冲突。

第一版实现 `JSONFileStore`：

- 写入临时文件；
- `fsync` 后原子 rename；
- 保存最近至少两份 JSON 热更新历史快照；
- 启动时拒绝损坏或校验失败的配置；
- API Key 不进入该 Store、运行时 JSON 或配置历史；只保存独立 SecretStore 的引用。

热更新 Store 明确绑定 JSON 文件格式，因为 JSON 同时满足机器写入、Web 面板编辑和人工审阅。未来如需数据库持久化，只替换 Store，不改变 runtime 层。静态 YAML 仍由 Loader 负责，不纳入 Store 写回流程。

## 4. Runtime Manager

建议接口：

```go
type Manager interface {
    Snapshot() RuntimeStatus
    Apply(context.Context, config.Snapshot) (ApplyResult, error)
    Restart(context.Context, RestartReason) error
    Shutdown(context.Context) error
}
```

`Apply` 按变更影响选择动作：

| 变更 | 动作 |
| --- | --- |
| 日志等级 | 原地更新 |
| LLM 协议、Base URL、模型、密钥 | 创建新 `llm.Client`，成功后原子替换 |
| Bot/Gate 策略 | 重建 Engine 或受控更新 |
| 平台 token、webhook、文档根目录 | 软重启相关 runtime |
| HTTP 地址、认证根密钥、配置路径 | 拒绝热应用，标记需进程重启 |

应用顺序：

1. 对候选快照执行完整校验；
2. 计算变更影响；
3. 对可预检组件先创建新实例；
4. 进入单写锁，阻止并发应用；
5. 原子替换可热切组件；
6. 对需要重启的组件执行优雅停止和启动；
7. 更新状态、历史和审计结果；
8. 失败时回滚组件引用及配置快照。

LLM 特别处理：

```text
旧 llm.Client 保持服务
        ↓
构造新协议 client
        ↓ 成功
原子替换引用
        ↓
旧 client 等待已有请求结束后释放
```

## 5. Bot Engine 依赖边界

Engine 不得调用 `llm.NewClient` 或读取配置文件。改为通过依赖结构注入：

```go
type Dependencies struct {
    Sender platform.Sender
    LLM    llm.Client
    Policy bot.Policy
}
```

这样 LLM、sender、gate 和未来 search 都能由 `runtime.Manager` 构造与替换。

## 6. 管理 API 预留

第一版后端提供面板所需的 API 契约，前端另行实现：

- `GET /v1/admin/config`：当前脱敏配置和版本；
- `GET /v1/admin/config/history`：历史版本摘要；
- `POST /v1/admin/config/validate`：只校验不应用；
- `PUT /v1/admin/config`：带版本号更新应用层配置；
- `POST /v1/admin/config/rollback`：回滚指定版本；
- `GET /v1/admin/runtime`：runtime 状态、当前动作、错误和更新时间；
- `POST /v1/admin/runtime/restart`：显式软重启。

管理 API 的认证必须在路由层完成；业务层只接收已认证的变更请求。认证根密钥归启动 YAML/环境变量，不允许普通应用配置更新请求覆盖。

LLM API Key 使用独立契约：

- `PUT /v1/admin/llm/api-key`：写入或替换单个密钥；
- `DELETE /v1/admin/llm/api-key`：显式删除密钥；
- 所有 GET 接口均不提供密钥正文；
- 普通 `PUT /v1/admin/config` 出现 `api_key` 字段时按未知/非法字段拒绝。

## 7. 故障与恢复

- 新 LLM client 构造失败：旧 client 继续服务；
- runtime 重启超时：停止继续扩散，状态为 `degraded`，保留可回滚快照；
- JSON 热更新文件写入失败：不发布内存快照；
- 进程重启读取到损坏的 YAML 或 JSON：优先尝试 JSON 历史快照；静态 YAML 仍无效时拒绝启动；
- 应用请求在切换期间：已有请求使用旧实例完成，新请求使用新实例；软重启期间由 runtime 状态决定接受或拒绝。

## 8. 测试策略

- 配置分类、默认值、脱敏和版本冲突单测；
- FileStore 原子写入、损坏恢复和回滚测试；
- Runtime Manager 的原地更新、LLM 原子替换、软重启和失败恢复测试；
- 管理 API 的校验、并发更新、权限和状态码测试；
- 使用 fake LLM、fake adapter、fake clock，避免测试依赖网络和真实平台。
