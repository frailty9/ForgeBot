# Connector Provider 模块化需求

日期：2026-09-20。

状态：需求已于 2026-09-20 确认；当前进入技术设计，设计确认前不实施代码重构。

## 1. 背景与目标

上一阶段已将主动介入、结构化检索与知识反思收敛为内置功能模块；但通讯接入仍由应用组合根直接识别 `echo` 与 `onebot`，并解析 `ConnectorConfig.OneBot`。这意味着核心仍了解特定协议的参数、构建逻辑与脱敏规则，无法把通讯协议作为可独立启停、可扩展的原生模块。

本阶段将协议实现改造成 **Connector Provider Module**：模块提供按 `type` 构建连接器实例的能力，`platforms.connectors` 继续声明实际接入实例，`platforms.groups` 继续把外部目标绑定到逻辑会话。核心只处理实例通用元数据、Provider 查找、生命周期编排和绑定筛选，不再理解 OneBot 或 Echo 的私有字段。

本阶段完成后，内置 `native.onebot` 与 `native.echo` 都通过同一 Provider 契约服务于连接器实例。未来增加新的通讯协议，或以外部模块提供协议实现时，不应要求修改核心的连接器类型分支。

## 2. 术语与边界

- **Connector Provider Module**：注册一种连接器 `type` 的模块；它解析模块与实例私有参数、构建具体 `platform.Adapter`，并负责其公开配置视图。
- **连接器实例**：`platforms.connectors[]` 中的一个声明，代表一个协议账号或接入端点，例如 `qq-main`。实例用 `type` 选择 Provider。
- **逻辑会话绑定**：`platforms.groups[]` 中的声明，把实例侧目标（如 QQ 群号）映射到 ForgeBot 的 `conversation_id`。它必须保持协议无关。
- **模块默认参数**：`modules.native.<provider>.options`，适用于该协议类型的多个实例。
- **实例参数**：`platforms.connectors[].options`，由被选中的 Provider 原样解析。
- **核心**：配置载入、模块运行时、Runtime Manager、平台绑定与宿主生命周期；核心不解析特定协议参数，也不猜测敏感字段。

本阶段不实现外部插件进程、FPP、插件市场、插件 Web 表单或 MCP/Skill 行为变化；它们可在此 Provider 契约稳定后另行设计。也不改变 OneBot 11 的现有业务能力。

## 3. 目标配置模型

### 3.1 协议模块与实例声明分层

`modules` 负责声明协议实现是否可用及其默认参数；`platforms.connectors` 负责声明具体接入实例。两者不可互相替代：模块不是账号实例，连接器也不携带协议实现代码。

```json
{
  "modules": {
    "native.onebot": {
      "enabled": true,
      "options": {"default_timeout": "15s"}
    },
    "native.echo": {"enabled": true, "options": {}}
  },
  "platforms": {
    "connectors": [{
      "id": "qq-main",
      "type": "onebot",
      "enabled": true,
      "prompt_suffix": "当前回复将发送到不支持 Markdown 的聊天渠道，请使用纯文本。",
      "options": {
        "api_url": "http://127.0.0.1:3000",
        "send_token": "",
        "receive_secret": "",
        "webhook_addr": "127.0.0.1:3333",
        "webhook_path": "/onebot/qq-main",
        "request_limit": 1048576,
        "timeout": "15s",
        "replay_ttl": "5m",
        "replay_max": 10000
      }
    }],
    "groups": [{"id": "support", "connector_id": "qq-main", "target": "853453909"}]
  }
}
```

`ConnectorConfig` 的公共形态应只包含 `id`、`type`、`enabled`、`prompt_suffix` 与 `options`；现有 `onebot` 嵌套字段及核心侧的 `OneBotConnectorConfig` 是破坏式删除目标。

### 3.2 两级参数的归属

Provider 接收模块参数和实例参数；它定义本协议的合并与默认值语义：

```text
native.onebot.options 的默认值
  -> qq-main.options 的显式覆盖
  -> 构建 qq-main 的 Adapter
```

核心不得合并或校验 OneBot、Echo 或任何未来协议的私有字段。Provider 也不得读取整个 RuntimeConfig、ConfigStore 或 SecretStore；它只能接收受限的构建请求。

## 4. 用户故事

1. 作为部署者，我可以启用 `native.onebot`，并在 `platforms.connectors` 声明多个 `type: onebot` 实例，使同一个二进制接入多个 OneBot 账号或端点。
2. 作为部署者，我可以只启用某个协议模块而暂不创建该类型实例，不让系统报错。
3. 作为部署者，我在关闭 `native.onebot` 后，若仍启用了 `type: onebot` 实例，可以在配置校验或更新阶段获得明确错误，而不会以半启动状态运行。
4. 作为部署者，我可以为协议设置模块级默认值，并让单个连接器实例覆盖其中一部分。
5. 作为管理面使用者，我可以查看连接器的公开配置与运行状态，但不会从 API 获得 OneBot 发送 Token、接收密钥或未来 Provider 认定为敏感的原始字段。
6. 作为协议实现者，我可以通过注册一个 Provider 支持新 `type`，无需向核心增加 `switch connector.Type` 分支。
7. 作为现有 OneBot 使用者，我在完成本阶段明确的配置迁移后，仍可以收到群消息、解析现有支持的内容、并发送回复和运营通知。

## 5. 验收标准

### 5.1 Provider 注册与依赖

- 当模块运行时构建候选配置时，系统应收集已启用模块提供的 Connector Provider。
- 当一个已启用连接器的 `type` 没有可用 Provider 时，系统应拒绝该候选配置，并指出连接器 ID、类型及缺少或未启用的 Provider。
- 当 `native.onebot` 或 `native.echo` 已启用但没有对应实例时，系统应接受配置。
- 当两个已启用模块尝试注册相同连接器类型时，系统应拒绝候选配置而非按注册顺序静默覆盖。
- 核心不得对外部 Provider 的业务能力或“是否等价替代原生功能”做契约审查；仅做类型可用性、标识冲突和基础技术依赖校验。

### 5.2 配置与公开视图

- 当读取 Runtime JSON 时，系统应把 `platforms.connectors[].options` 保留为可由 Provider 解析的原始 JSON 值，并继续校验公共字段与群组绑定。
- 当 Provider 校验私有参数失败时，系统应返回包含连接器实例定位信息的错误。
- 当管理 API 返回运行时配置时，系统应调用相应 Provider 的公开配置转换逻辑；核心不得通过字段名称、反射或固定敏感字段表进行脱敏。
- 对 OneBot，公开视图应使用 `send_token_configured` 与 `receive_secret_configured` 表达密钥是否已配置，绝不回显原值。
- 本项目尚未发布正式版本；旧 `platforms.connectors[].onebot` 结构无需兼容。

### 5.3 构建与生命周期

- 当 Runtime Manager 构建一个连接器集合时，核心应将该实例的模块参数、实例参数、公共元数据和已筛选的群组绑定传给选定 Provider。
- Provider 应仅为属于该连接器 ID 的绑定构建协议侧路由，不得自行扫描全局群组。
- 当配置热更新只涉及连接器实例、协议模块参数或绑定时，Runtime Manager 应按现有连接器重启语义停止旧集合、启动新集合，并在启动失败时恢复旧集合。
- 候选 generation 的准备阶段不得提前启动 Webhook listener、抢占端口或对外发送。
- Echo 与 OneBot 都应经由 Provider 创建；应用组合根及 Runtime 核心中不得保留按 `echo`、`onebot` 类型实例化 Adapter 的分支。

### 5.4 OneBot 行为保持

- 当迁移后的 OneBot 连接器接收有效的 OneBot 11 webhook 时，它应保持现有的认证、重放保护、群组映射、引用与媒体解析及出站消息能力。
- 当 OneBot 的模块级默认参数与实例参数同时提供时，实例参数应按 Provider 规则覆盖默认参数；缺失的实例参数应能采用有效默认值。
- 当构建或启动 OneBot 失败时，错误日志和管理状态应继续能够定位具体连接器 ID，且不泄漏密钥值。

## 6. 约束与非目标

- 保留 `platforms.groups`：它是协议无关的目标到逻辑会话绑定层，不能迁入 OneBot 模块 options。
- 保留 `platforms.connectors`：它是接入实例清单，不能由模块配置替代。
- 本阶段可以破坏式更新示例、文档及现有本地 JSON；不实现旧字段迁移层。
- 本阶段不拆分配置文件，也不实现 `ConfigStore`、`FileStore`、`DBStore` 等新的存储抽象；现有 JSONFileStore 保持不变。
- 不在本阶段引入新通讯协议的完整实现；Echo 的迁移用于验证抽象，OneBot 的迁移是主目标。
- 不在本阶段改变 Bot 核心只面向逻辑会话、而不感知协议账号与群组实现细节的原则。

## 7. 测试范围

- 配置解析、公共字段校验、Provider 缺失、重复类型和 Provider 私有参数报错。
- 模块默认参数与实例覆盖参数的合并行为。
- OneBot/Echo Provider 构建、群组绑定筛选、公开视图脱敏。
- Runtime 热更新的启动、失败回滚与 ConnectorSet 生命周期。
- `go test ./...` 与 `go vet ./...`；Windows 上 `go test -race ./...` 的系统级启动限制应如实记录，不把二进制加载失败误报为业务测试失败。
