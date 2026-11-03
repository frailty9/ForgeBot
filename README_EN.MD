# ForgeBot

[中文](README_ZH.MD) · [Configuration](docs/configuration.md) · [CLI guide](docs/cli.md)

ForgeBot is a general-purpose AI Bot for group collaboration. Combine question answering, knowledge tools and community operations through configuration, backed by a Go runtime with replaceable connectors, feature modules and hot updates.

The project is preparing a `v0.1.0` source preview and has not been released. OneBot 11 and a local Echo connector are available. WebUI, FPP external plugins, Telegram, SQL/NoSQL backends and vector retrieval are deferred.

## Capabilities

- Multiple connectors and groups, identified by protocol-neutral conversation IDs.
- Exactly three answer protocols: Anthropic Messages, OpenAI Responses and OpenAI Chat Completions.
- Text, quotations, explicitly enabled image understanding, empty mentions and reliable emoji descriptions.
- Bounded multi-round tool execution through native Tools and MCP over Streamable HTTP or stdio; local Skills contribute instructions.
- On-demand MD, MDX and TXT retrieval through tools, without searching for every message.
- Short-term context isolated by group and user, held in memory without persisting full group history.
- Optional decision models with an isolated answer-LLM fallback, configurable business scope and evidence collection.
- Cautious proactive participation with group-level yielding when a trusted administrator answers; explicit mentions retain normal processing rules.
- Periodic reflection that turns administrator answers into Markdown at a configurable location.
- Optional join review, text announcements, message recall and welcome templates, with explicit unsupported results.
- Versioned configuration, history, rollback, component replacement, encrypted write-only credential management and a read-only capability catalog.

Implementation and live acceptance are tracked separately in the [default behavior matrix](docs/default-behavior.md) and [version tasks](specs/v0.1.0/tasks.md).

## First run

Use Go 1.25.5 or a newer version capable of building the project, plus an answer-model endpoint. Run these PowerShell commands from the repository root:

```powershell
go run ./cmd/forgebot init -dir configs -model YOUR_MODEL -protocol openai_completions -base-url https://YOUR_API_ROOT
$env:FORGEBOT_SECRET_KEY = (Get-Content -Raw configs/master.local.key).Trim()
$env:FORGEBOT_LLM_API_KEY = 'YOUR_PROVIDER_API_KEY'
go run ./cmd/forgebot -config configs/forgebot.local.yaml -init-api-key llm/default
Remove-Item Env:FORGEBOT_LLM_API_KEY
go run ./cmd/forgebot doctor -config configs/forgebot.local.yaml
go run ./cmd/forgebot -config configs/forgebot.local.yaml
```

Replace the uppercase placeholders. `base-url` is the protocol API root; ForgeBot appends the protocol endpoint. Image understanding defaults to false; pass `-images` for a vision-capable model. Use `-anonymous` only for an explicitly unauthenticated local service. Back up the master key and encrypted credentials, storing them separately; never commit `master.local.key`.

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

Licensed under [Apache-2.0](LICENSE). Third-party components retain their [own licenses and notices](THIRD_PARTY_NOTICES.md). `v0.1.0` has not been published; real QQ join review, announcements, recall, welcome events and optional DM behavior still require live acceptance. Controlled-service tests do not establish actual account permissions.
