# ForgeBot

[中文](README_ZH.MD) · [Configuration](docs/configuration.md) · [CLI guide](docs/cli.md)

ForgeBot is a general-purpose AI Bot for group collaboration. Combine question answering, knowledge tools and community operations through configuration, backed by a Go runtime with replaceable connectors, feature modules and hot updates.

**One deployment serves one business.** A single ForgeBot instance can join many groups across several platforms, but they all share one business scope (`bot.system_prompt`), one knowledge base, one answer model and one set of feature rules such as join review; individual groups only choose which features to turn on. To serve several unrelated businesses, run one instance per business.

`v0.1.0` is merged and locally tagged; `v0.2.0` is in development. OneBot 11, local Echo, and an [official QQ group connector](docs/qqbot.md) are implemented; live acceptance for the new connector remains pending. A [Telegram group connector](specs/v0.2.0/requirements.md#15-telegram-群聊-connector2026-11-11-新增) is now planned for v0.2.0 and is not implemented. WebUI, FPP external plugins, SQL/NoSQL backends and vector retrieval are deferred.

## Capabilities

- Multiple connectors and groups, identified by protocol-neutral conversation IDs.
- Exactly three answer protocols: Anthropic Messages, OpenAI Responses and OpenAI Chat Completions.
- Text, quotations, explicitly enabled image understanding, empty mentions and reliable emoji descriptions.
- Bounded multi-round tool execution through native Tools and MCP over Streamable HTTP or stdio; local Skills contribute instructions.
- On-demand MD, MDX and TXT retrieval through tools, without searching for every message.
- Short-term context isolated by group and user, held in memory without persisting full group history.
- Shared model decisions using DM when enabled or an isolated answer LLM otherwise, with at most one request per decision, used for proactive replies, administrator takeover and join review; business scope is passed to the answer model as instructions.
- Cautious proactive participation with group-level yielding when a trusted administrator answers; explicit mentions retain normal processing rules.
- Bounded administrator reply sessions, resolved from recent history or quoted text, periodically reflected into Markdown after a quiet window at a configurable location.
- Optional automatic join review combining regex, external HTTP and model checks with AND/OR, plus welcome templates and explicit unsupported results.
- A host capability for recalling one message by ID within a bound conversation; triggering and moderation policy are left to consuming features.
- Versioned configuration, history, rollback, component replacement, encrypted write-only credential management and a read-only capability catalog.

Implementation and live acceptance are tracked separately in the [default behavior matrix](docs/default-behavior.md) and [version tasks](specs/v0.2.0/tasks.md).

## First run

Use Go 1.27 or newer, plus an answer-model endpoint. Run these PowerShell commands from the repository root:

```powershell
go run ./cmd/forgebot init -dir configs -model YOUR_MODEL -protocol openai_completions -base-url https://YOUR_API_ROOT
$env:FORGEBOT_LLM_API_KEY = 'YOUR_PROVIDER_API_KEY'
go run ./cmd/forgebot -config configs/forgebot.local.yaml -init-api-key llm/default
Remove-Item Env:FORGEBOT_LLM_API_KEY
go run ./cmd/forgebot doctor -config configs/forgebot.local.yaml
go run ./cmd/forgebot -config configs/forgebot.local.yaml
```

Replace the uppercase placeholders. `base-url` is the protocol API root; ForgeBot appends the protocol endpoint. Image understanding defaults to false; pass `-images` for a vision-capable model. Use `-anonymous` only for an explicitly unauthenticated local service. `init` writes the master key and admin token into `forgebot.local.yaml`; back up that file and the encrypted credentials, and never commit them.

The default Echo connector logs outbound replies and does not connect to a social platform. Send a UTF-8 test request:

```powershell
$body = @{conversation_id='local'; user_id='u1'; message_id='test-1'; text='Hello, introduce yourself briefly'; mentioned=$true} | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/v1/messages -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($body))
```

The HTTP response contains processing status; find the answer in logs or the connected platform. This endpoint is not a chat-completion API. For QQ, follow the [configuration guide](docs/configuration.md) to enable `native.onebot`, declare a connector, bind groups and configure both event reporting and the OneBot action service.

## Configuration and management

Static YAML selects server settings, backend and file locations. Runtime JSON controls application behavior. A normal run requires an answer model and its credentials; a dedicated decision model is optional. API keys remain encrypted and are not returned in ordinary configuration, history or read endpoints.

Editing a JSON file does not automatically update the running instance. Submit an authenticated configuration update or restart the process. A Bot-thread restart rebuilds the current in-memory snapshot without re-reading the file. Updates use `expected_version`; failed candidates leave the current generation active. Each process selects one file backend in this release.

```powershell
go run ./cmd/forgebot capabilities -config configs/forgebot.local.yaml
```

Management requests require `Authorization: Bearer <admin_token>`. APIs cover configuration, history, rollback, runtime restart, index reload, reflection scans, answer/DM credentials and group actions. A Web administration frontend is not bundled.

## Documentation and development

- [Configuration and MCP tutorial](docs/configuration.md)
- [CLI initialization, diagnosis and capabilities](docs/cli.md)
- [Group administration and welcome messages](docs/group-management.md)
- [Decision contract](docs/decision-service.md)
- [Storage and recovery](docs/storage.md)
- [Core operation results](docs/operation-results.md)
- [Repository layout](docs/repository-layout.md) and [architecture](docs/architecture.md)

```powershell
go test ./...
go test -race ./...
go vet ./...
```

Track `docs/` for current usage and boundaries, and `specs/` for requirements, design, tasks and acceptance. Keep deployment configuration, credentials, logs, indexes, generated knowledge and personal AI settings out of Git. Internal modules and Hooks support customization; no stable public SDK or FPP implementation is currently promised.

## License and acceptance status

Licensed under [Apache-2.0](LICENSE). Third-party components retain their [own licenses and notices](THIRD_PARTY_NOTICES.md). `v0.1.0` has not been published; real QQ join review, welcome events, how well the target model follows scope instructions and tool retrieval, and optional DM behavior still require live acceptance. Group announcements and message recall are suspended and out of scope for this release. Controlled-service tests do not establish actual account permissions.
