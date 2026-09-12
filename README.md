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
- On-demand local MD/MDX structured search with atomic hot reload.
- A restricted, allow-listed tool executor; ordinary messages do not search
  unless the model requests `search_docs`.
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
        "timeout": "15s",
        "replay_ttl": "5m",
        "replay_max": 10000
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
Bearer token on outbound API calls. Platform setting changes rebuild and
replace the Connector generation through `runtime.Manager`; failed startup or
persistence restores the previous active connectors.

Accepted OneBot message events are deduplicated by connector/account/group/user/message
identity. `replay_ttl` controls the replay window and `replay_max` bounds memory
usage. Array message segments are preferred for clean text extraction; CQ codes
are removed when only `raw_message` is available.

Operator groups are an allow-listed, protocol-neutral operations boundary.
They reference an existing logical conversation and never inherit the full Web
administration API. In an operator group, slash commands are intercepted before
ordinary Bot handling; non-command messages continue through the normal Bot
pipeline.

Available operator-group commands are `/status`, `/restart`, `/pause`,
`/resume`, `/gate on|off`, and `/reload-docs`. Restart, Gate, and document
reload operations acknowledge first, then report completion after the runtime
operation finishes.
Runtime, Connector, and LLM failures are dispatched asynchronously to operator
groups with `notifications: true`; delivery failure is logged and never blocks
the component that emitted the event.

## Local Document Search

Search is disabled by default. Enable it in runtime JSON and set an absolute
local document root:

```json
{
  "search": {
    "enabled": true,
    "policy": "on_demand",
    "backend": "structured",
    "document_root": "D:/Project/ForgeBot/forgebot-docs",
    "extensions": [".md", ".mdx"],
    "default_results": 5,
    "max_results": 8,
    "min_match_ratio": 0.35,
    "query_timeout": "750ms",
    "build_timeout": "20s",
    "max_queries_per_turn": 1,
    "max_context_chars": 12000,
    "max_section_chars": 8000,
    "max_file_bytes": 4194304,
    "max_total_bytes": 268435456,
    "max_files": 10000
  }
}
```

Ordinary questions go to the LLM without a preliminary index query. The LLM
may request the read-only `search_docs` tool when project facts are needed;
the default budget permits one search and at most two LLM requests per turn.
Use `/search <query>` to skip that decision and search immediately. Search
results enter the prompt as bounded, untrusted reference data with source
paths. Vector and hybrid backends are intentionally unavailable at this stage.

Changing Search configuration builds a complete candidate index before the
new runtime JSON is persisted. A failed build leaves the old configuration and
index active. `/reload-docs` and the admin reload endpoint rebuild the current
index atomically without changing the configuration version or restarting
platform connectors.

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
POST   /v1/admin/search/reload
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
