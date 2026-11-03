# 工程目录

| 目录 | 用途 | 是否提交 |
| --- | --- | --- |
| `cmd/forgebot/` | 可执行入口与 CLI | 是 |
| `internal/` | Core 契约、运行时、领域实现与内置模块 | 是 |
| `configs/*.example.*` | 无真实凭据的配置示例 | 是 |
| `docs/` | 当前使用、配置、架构、排障和边界 | 是 |
| `specs/` | 需求、设计、任务、验收、历史交接 | 是 |
| 测试包内夹具 | 可公开的确定性测试输入 | 是 |
| `configs/*local*`、`master.local.key` | 本地部署配置与主密钥 | 否 |
| `data/`、`logs/`、`cache/`、`tmp/`、`media/` | 运营状态、日志、索引、临时媒体 | 否 |
| 部署知识库、自动沉淀目录 | 用户数据；建议放工作区外或明确忽略的部署目录 | 否 |
| `.claude/settings.local.json` 等 | 个人工具设置 | 否 |

`docs` 和 `specs` 不按文件后缀区分，按用途区分。产品教程不依赖部署者阅读计划；规格即使完成也保留设计和验收证据。旧规格不自动覆盖当前行为；以最新需求与实现交接为准。用户之前提到的 space 已确认为 specs，不增加重复目录。

不采用“忽略所有 JSON/YAML/Markdown”的规则。共享项目规范与示例仍应提交，个人配置和生成文件使用具体路径。`init` 在目标目录生成本地忽略规则，已有 `.gitignore` 不覆盖；自选其他部署位置时应检查密钥与数据确实未被跟踪。

Core 的操作分类、群事件、会话、决策和存储使用明确契约。`internal/modules` 是可配置原生功能；`native.onebot` 贡献协议 Provider，实际账号/端点仍由 connectors 选择，逻辑群由 groups 绑定。MCP/Skill 是 Core Agent 扩展入口，不搬到普通功能模块中。
