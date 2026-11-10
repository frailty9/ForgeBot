# ForgeBot

[English](README_EN.MD) · [配置说明](docs/configuration.md) · [初始化与诊断](docs/cli.md)

ForgeBot 是面向群组协作的通用 AI Bot：通过配置组合问答、知识工具、社区协作和运营功能，并以 Go 运行时提供可替换连接器、模块化功能与热更新。

**一个部署对应一个业务。** 一个 ForgeBot 实例可以同时接入多个平台的多个群，但它们共用同一份业务范围（`bot.system_prompt`）、知识库、回答模型和功能规则（如入群审批规则），各群只决定开启哪些功能。需要服务多个互不相关的业务时，请为每个业务各部署一个实例。

当前正在准备 `v0.1.0` 源码预览，尚未正式发布。首版包含 OneBot 11 和用于本地验证的 Echo 连接器；WebUI、FPP 外部插件协议、Telegram、SQL/NoSQL 后端和向量检索留待后续迭代。

## 可以做什么

- 多连接器、多群同时运行；Bot 核心使用逻辑群标识。
- 三种回答模型协议：Anthropic Messages、OpenAI Responses、OpenAI Chat Completions。
- 文字、引用、图片问答与空 @；图片理解需显式启用，平台表情尽量转成可靠文字。
- 有预算的多轮工具调用，内置 Tools 与 MCP（Streamable HTTP / stdio）共用执行边界；本地 Skill 提供额外指令。
- 按需检索 MD、MDX、TXT 文档；知识检索属于工具能力，不默认每条消息先查库。
- 按群和用户隔离的短期问答上下文，默认进程内保留，不保存完整群聊历史。
- 共享模型决策：有 DM 用 DM，无 DM 用独立 LLM，每次最多一次请求，用于主动插话、管理员接答和入群审批；业务范围以提示词交给回答模型。
- 按群谨慎主动介入；管理员接答时临时让位，明确 @ 仍遵守正常问答规则。
- 管理员引用答复形成有界会话，静默后定时沉淀为 Markdown；问题先查近期消息，再用引用原文，可配置输出位置。
- 可选入群申请自动审批与新人欢迎模板；不支持的连接器明确报告限制。
- 版本化运行配置、历史/回滚、组件热替换、加密凭据只写管理与只读能力目录。

功能实现与真实环境验收分开记录，见[默认行为与验收](docs/default-behavior.md)和[版本任务](specs/v0.1.0/tasks.md)。

## 首次启动

需要 Go 1.27 或更新版本，以及一个可用的回答模型服务。以下为 PowerShell 示例，在仓库根目录执行：

```powershell
go run ./cmd/forgebot init -dir configs -model YOUR_MODEL -protocol openai_completions -base-url https://YOUR_API_ROOT
$env:FORGEBOT_LLM_API_KEY = 'YOUR_PROVIDER_API_KEY'
go run ./cmd/forgebot -config configs/forgebot.local.yaml -init-api-key llm/default
Remove-Item Env:FORGEBOT_LLM_API_KEY
go run ./cmd/forgebot doctor -config configs/forgebot.local.yaml
go run ./cmd/forgebot -config configs/forgebot.local.yaml
```

替换大写占位值。`base-url` 是协议 API 根地址，程序追加对应端点；初始化缺省图片能力关闭，支持图片的模型可增加 `-images`。无认证本地服务用 `-anonymous`，不要假填 Key。`init` 把主密钥和管理令牌写进 `forgebot.local.yaml`；请单独备份该文件和加密凭据，不要提交。

首次启动默认使用 Echo：它接收入站测试并将出站结果写到日志，不连接社交平台。测试请求使用 UTF-8 字节，避免 PowerShell 编码变成问号：

```powershell
$body = @{conversation_id='local'; user_id='u1'; message_id='test-1'; text='你好，请简单介绍自己'; mentioned=$true} | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/v1/messages -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($body))
```

HTTP 返回处理结果，回答内容查看日志或社交平台；它不是返回聊天文本的 Chat API。接入 QQ 需按[配置说明](docs/configuration.md)启用 `native.onebot`、声明连接器并绑定群组，同时设置 OneBot 事件上报和动作服务地址。

## 配置与管理

静态 YAML 管服务器、后端和文件位置；JSON 管应用行为。正常运行必须配置回答模型与所需凭据，决策模型可缺省。API Key 进入加密文件仓储，不在普通配置、历史或读接口中返回。

修改 JSON 文件不会自动改变运行中实例；通过受认证的配置 API 提交，或进程重启后加载。Bot 线程 restart 重建当前内存快照，不重新读取磁盘文件。配置更新使用 `expected_version`，候选失败保留当前实例；首版每个进程只选一个文件后端。

```powershell
go run ./cmd/forgebot capabilities -config configs/forgebot.local.yaml
```

管理接口使用 `Authorization: Bearer <admin_token>`。API 包含配置、历史、回滚、运行时重启、文档索引重载、沉淀扫描、模型/DM 凭据和群操作。当前无内置 Web 管理面板。

## 文档与开发

- [完整配置与 MCP 教程](docs/configuration.md)
- [CLI 初始化、诊断和能力目录](docs/cli.md)
- [群管理与欢迎语](docs/group-management.md)
- [共享决策契约](docs/decision-service.md)
- [存储和恢复](docs/storage.md)
- [Core 结果分类](docs/operation-results.md)
- [目录约定](docs/repository-layout.md)与[架构](docs/architecture.md)

```powershell
go test ./...
go test -race ./...
go vet ./...
```

`docs/` 记录当前使用方法和边界，`specs/` 记录需求、设计、任务和验收，二者均提交。部署配置、凭据、日志、索引、自动沉淀文档和个人 AI 设置不应进入仓库。内部模块与 Hook 可扩展功能，但当前没有稳定公开 SDK 或 FPP 实现。

## 许可证与验收状态

采用 [Apache-2.0](LICENSE)，第三方组件保留各自[许可证与归属](THIRD_PARTY_NOTICES.md)。目前不宣称已发布 `v0.1.0`；真实 QQ 账号的入群审批、新人事件、目标模型对范围提示词与检索的遵守，以及可选 DM 仍需单独验收。群公告与消息撤回已挂起，不在本版范围内。自动测试使用受控服务，不能证明部署账号具有实际权限。
