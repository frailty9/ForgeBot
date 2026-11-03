# 初始化、诊断与能力目录

所有命令放在程序路径之后、命令参数之前。初始化不启动机器人，也不调用模型：

```powershell
go run ./cmd/forgebot init -dir configs -model YOUR_MODEL -protocol anthropic_messages -base-url https://YOUR_API_ROOT
```

支持三种既有 LLM 协议。图片能力缺省 false，模型支持图片时加 `-images`；无认证本地模型加 `-anonymous`，不能靠漏填 Key 自动启用匿名。目录会生成 `forgebot.local.yaml`、`runtime.local.json`、`master.local.key` 和本地忽略规则（已有 `.gitignore` 不覆盖）。Key 文件与配置使用受限文件模式；Windows 权限仍由所在目录的 ACL 决定。已有目标文件或凭据时拒绝覆盖，不替换密钥。

加载本地主密钥，再首次写入回答 Key：

```powershell
$env:FORGEBOT_SECRET_KEY = (Get-Content -Raw configs/master.local.key).Trim()
$env:FORGEBOT_LLM_API_KEY = 'YOUR_PROVIDER_API_KEY'
go run ./cmd/forgebot -config configs/forgebot.local.yaml -init-api-key
Remove-Item Env:FORGEBOT_LLM_API_KEY
```

主密钥文件应单独备份，不提交 Git；迁移部署时应连同加密凭据一起保留。正式启动仍通过环境变量读取主密钥，不会自行读取任意 key 文件。

```powershell
go run ./cmd/forgebot doctor -config configs/forgebot.local.yaml
go run ./cmd/forgebot -config configs/forgebot.local.yaml
```

`doctor` 可在模型未配齐时运行，输出缺项并以非零状态退出。默认验证配置、原生模块、连接器参数、仓储路径、凭据、Skill 目录和本地端口；不启动 Bot/连接器，不创建索引，不恢复或写入数据文件，不发群消息。它短暂绑定本地监听端口确认占用情况，已运行的实例占用端口会显示失败。默认结果只代表本地准备状态，不证明远端模型、账号权限或知识库效果可用。

主动探测必须显式选择：

```powershell
go run ./cmd/forgebot doctor -config configs/forgebot.local.yaml -probe
go run ./cmd/forgebot doctor -config configs/forgebot.local.yaml -probe-tools
```

`-probe` 发一个有界的回答模型完成请求，可能消耗额度；`-probe-tools` 初始化 MCP HTTP 或执行已配置的 stdio 命令、列出工具，完成后关闭，不调用工具。探测共享 30 秒总预算、单项最多 15 秒。它们不审核申请、不发公告、不撤回消息。当前不进行专用 DM 的真实探测，DM 单独验收。

运行后可查看当前能力：

```powershell
go run ./cmd/forgebot capabilities -config configs/forgebot.local.yaml
```

命令从运行中实例的受认证 `GET /v1/admin/capabilities` 读取，与管理 API 共用同一投影。地址缺省来自静态 server.addr；连接到其他地址时显式传 `-url http://127.0.0.1:8080`。输出区分 enabled/configured/loaded/health，并包含类型、来源、权限可知性和文档入口。目录不主动探测网络，`not_probed` 不能当作健康；Skill 是加载快照，空目录不会伪装已加载技能。输出不返回凭据或凭据引用，也不公开 stdio 环境变量。
