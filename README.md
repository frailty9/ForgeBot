# ForgeBot

ForgeBot is a clean-room multi-platform bot foundation for ForgeBot.

The project starts from a small, testable core instead of inheriting the
historical MuseBot runtime. Platform adapters convert external events into a
single message model; the bot engine applies admission control, routing,
retrieval, tools, and LLM providers through explicit interfaces.

## Current Scope

- YAML startup configuration plus writable JSON runtime configuration.
- Structured runtime lifecycle with graceful shutdown.
- HTTP management and test ingress.
- Unified inbound/outbound message contracts.
- Platform sender registry.
- OneBot 11 HTTP webhook adapter with outbound `send_msg` support.
- Reusable group message gate with buffering, deduplication, and stats.
- A deliberately small LLM boundary supporting Anthropic Messages, OpenAI
  Responses, and OpenAI Chat Completions, including normalized JSON and SSE
  clients.

The OneBot adapter is wired for startup when enabled in runtime JSON. It
remains replaceable behind `platform.Adapter` and `platform.Sender`.

Example OneBot runtime settings:

```json
{
  "platforms": {
    "connectors": [{
      "id": "qq-main",
      "type": "onebot",
      "enabled": true,
      "onebot": {
        "api_url": "http://127.0.0.1:5700",
        "send_token": "",
        "receive_secret": "",
        "webhook_addr": "127.0.0.1:8090",
        "webhook_path": "/onebot/qq-main",
        "request_limit": 1048576,
        "timeout": "15s"
      }
    }],
    "groups": [
      {"id": "support", "connector_id": "qq-main", "target": "10001"},
      {"id": "engineering", "connector_id": "qq-main", "target": "10002"}
    ]
  }
}
```

OneBot sends POST callbacks to the configured webhook path. `receive_secret`
validates its `X-Signature` using HMAC-SHA1, while `send_token` is sent as a
Bearer token on outbound API calls. Platform settings are currently applied at process startup; runtime
adapter replacement is part of the Runtime lifecycle follow-up.

Operator groups are an allow-listed, protocol-neutral operations boundary.
They reference an existing logical conversation and never inherit the full Web
administration API. In an operator group, slash commands are intercepted before
ordinary Bot handling; non-command messages continue through the normal Bot
pipeline. Command implementations remain disabled until the Runtime lifecycle
can safely execute them.

## Quick Start

```powershell
go test ./...
go run ./cmd/forgebot -config configs/forgebot.example.yaml
```

Health check:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz
```

### LLM protocol selection

The `llm.protocol` field accepts exactly:

- `anthropic_messages` (`POST /v1/messages`)
- `openai_responses` (`POST /responses`)
- `openai_completions` (`POST /chat/completions`)

Use `llm.base_url` for a compatible gateway or vendor endpoint. The provider
name is not encoded into the core API; a vendor is selected by choosing the
protocol it implements.

The LLM API key is intentionally absent from both example files. It will be
managed through a dedicated write-only secret API and `SecretStore`; ordinary
configuration reads and history never expose it. ForgeBot does not load a
`.env` file. Deployment environments may use variables only for documented
startup overrides such as `FORGEBOT_HTTP_ADDR`.

The management API requires a Bearer token. Set `FORGEBOT_ADMIN_TOKEN` in the
deployment environment; it overrides `admin_token` in the startup YAML. The
token is never included in configuration responses.

The file-backed `SecretStore` encrypts values with AES-256-GCM. Its encryption
key is supplied as a Base64-encoded 32-byte value in `FORGEBOT_SECRET_KEY`; the
key itself must remain in the deployment environment and must not be written to
either configuration file.

To enable API key management, set `secret_store_path: secrets.json` in the
startup YAML and inject a generated master key before starting ForgeBot:

```powershell
$bytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
$env:FORGEBOT_SECRET_KEY = [Convert]::ToBase64String($bytes)
```

Configuration precedence and ownership are fixed:

```text
documented environment override > startup YAML > code default
runtime JSON is independent and is never overridden by environment variables
```

Each runtime JSON commit retains the previous valid document as
`runtime.json.bak`. If the primary document is damaged at startup, ForgeBot
loads that backup and reports `json_backup` as the source.

To migrate the original single JSON file, move `server` into the startup YAML,
move `log`, `bot`, `llm`, `gate`, and `platforms` into the runtime JSON, replace
`llm.api_key` with `llm.api_key_ref`, and submit the actual key through the
write-only secret endpoint.

### Management API

All management requests use `Authorization: Bearer <FORGEBOT_ADMIN_TOKEN>`.
The API supports current configuration, validation, versioned updates, history,
rollback, runtime restart, and write-only LLM API key management:

```text
GET    /v1/admin/config
GET    /v1/admin/config/history
POST   /v1/admin/config/validate
PUT    /v1/admin/config
POST   /v1/admin/config/rollback
GET    /v1/admin/runtime
POST   /v1/admin/runtime/restart
GET    /v1/admin/llm/api-key
PUT    /v1/admin/llm/api-key
DELETE /v1/admin/llm/api-key
```

The API key write body is a single-purpose payload. The response exposes only
configuration state, version, and update time:

```powershell
Invoke-RestMethod `
  -Method Put `
  -Uri http://127.0.0.1:8080/v1/admin/llm/api-key `
  -Headers @{ Authorization = "Bearer $env:FORGEBOT_ADMIN_TOKEN" } `
  -ContentType application/json `
  -Body '{"api_key":"replace-with-provider-key"}'
```

Send a test message through the HTTP ingress:

```powershell
Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:8080/v1/messages `
  -ContentType application/json `
  -Body '{"conversation_id":"local","user_id":"u1","message_id":"m1","text":"hello ForgeBot","mentioned":true}'
```

## Architecture

See [docs/architecture.md](docs/architecture.md).
