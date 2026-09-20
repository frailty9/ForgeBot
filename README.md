# ForgeBot

ForgeBot is an open-source, multi-platform bot runtime for building practical
LLM-powered assistants in group conversations.

It provides a protocol-neutral bot core, replaceable platform adapters, three
mainstream LLM wire protocols, on-demand local document search, controlled
operator actions, and runtime configuration that can be updated without
restarting the process.

ForgeBot is designed to run as a standalone service and does not require any
other product.

## What It Includes

- Explicit interfaces between the bot core, platform connectors, LLM clients,
  retrieval, tools, and runtime management.
- Multiple connectors and group bindings active at the same time.
- Module-level native feature switches backed by typed, immutable Hook chains.
  The first module controls empty-mention/image hints and search progress replies;
  external FPP plugins are not implemented yet.
- OneBot 11 HTTP webhook support with outbound `send_msg`, HMAC-SHA1 request
  verification, replay protection, and bounded request handling.
- A single logical conversation identity for the bot core, independent of the
  protocol, account, or external group ID.
- Group message gating with mention requirements, buffering, deduplication, and
  concurrency limits.
- LLM clients for exactly three protocols:
  - Anthropic Messages
  - OpenAI Responses
  - OpenAI Chat Completions
- On-demand structured search over local Markdown, MDX, and plain-text files
  using an in-memory BM25F index.
- An allow-listed `search_docs` tool. Ordinary messages do not trigger a
  document search unless the user explicitly uses `/search` or the model asks
  for the tool.
- Stateless knowledge reflection that can turn approved administrator answers
  into managed Markdown under the document root's `extra/` directory.
- Versioned JSON runtime configuration, optimistic concurrency control, atomic
  persistence, history, rollback, and component-level hot replacement.
- A write-only LLM API key management flow backed by an AES-256-GCM encrypted
  file store.

## Architecture

```text
platform adapter
    -> normalized inbound message
    -> logical group binding
    -> bot engine
    -> Admission / native modules / tools / LLM
    -> normalized outbound message
    -> platform sender
```

The core only sees a logical `conversation_id`. Platform-specific details stay
in connectors and bindings, so adding another protocol does not require changes
to the bot engine.

See [docs/architecture.md](docs/architecture.md) for package boundaries and
lifecycle contracts.

## Current Status

The current release is a focused runtime foundation rather than a complete
hosted product. OneBot 11 and an in-memory echo connector are available today;
additional platform adapters can implement the same interfaces. The structured
retriever currently supports local `.md`, `.mdx`, and `.txt` files only. Vector
and hybrid retrieval are intentionally not included yet.

The public HTTP surface includes health checks, test message ingress, and a
Bearer-protected management API. A web administration frontend is not bundled
in this repository.

## Quick Start

Requirements:

- Go 1.25 or newer
- A JSON runtime configuration file
- An LLM endpoint and API key for actual model responses

Run the test suite:

```powershell
go test ./...
```

Start the local example:

```powershell
go run ./cmd/forgebot -config configs/forgebot.example.yaml
```

The example starts the local echo connector and listens on `127.0.0.1:8080`.
Check the process without an LLM key:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz
```

To exercise a complete LLM turn, copy the example runtime configuration,
configure the provider, initialize the encrypted SecretStore with
`-init-api-key`, and start ForgeBot. The complete procedure and every field are documented in
[docs/configuration.md](docs/configuration.md).

Send a test message after an LLM key is configured:

```powershell
Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:8080/v1/messages `
  -ContentType application/json `
  -Body '{"conversation_id":"local","user_id":"u1","message_id":"m1","text":"hello ForgeBot","mentioned":true}'
```

## Minimal Runtime Configuration

Runtime settings live in writable JSON. The example below selects the OpenAI
Responses protocol; switch `protocol` to `openai_completions` or
`anthropic_messages` when using a compatible endpoint.

```json
{
  "bot": {"name": "ForgeBot"},
  "llm": {
    "protocol": "openai_responses",
    "base_url": "https://api.openai.com/v1",
    "api_key_ref": "llm/default",
    "model": "gpt-5",
    "timeout": "60s"
  },
  "platforms": {
    "connectors": [
      {"id": "echo-local", "type": "echo", "enabled": true}
    ],
    "groups": [
      {"id": "local", "connector_id": "echo-local", "target": "local"}
    ]
  }
}
```

`base_url` is the provider root. ForgeBot appends the protocol endpoint:

| Protocol | Endpoint |
| --- | --- |
| `anthropic_messages` | `<base_url>/v1/messages` |
| `openai_responses` | `<base_url>/responses` |
| `openai_completions` | `<base_url>/chat/completions` |

## Configuration And Operations

ForgeBot separates startup configuration from hot-updateable application
configuration:

- YAML and documented environment variables configure the HTTP server, runtime
  JSON path, SecretStore path, and management token.
- Runtime JSON configures the Bot, LLM, Admission, native modules, platforms,
  administration, and operations. Search and reflection are native modules.
- LLM API keys are stored separately and can be replaced without exposing their
  value in configuration reads or history.

Runtime updates are full replacements guarded by `expected_version`; validation
and candidate component startup happen before the new generation is published.
The management API supports configuration history, rollback, runtime restart,
document reload, reflection scans, and write-only API key management.

Read [docs/configuration.md](docs/configuration.md) before deploying. It covers
environment-variable precedence, path resolution, OneBot setup, search limits,
SecretStore initialization, hot updates, rollback, and production hardening.
The same guide includes an MCP tutorial for Streamable HTTP and stdio setup,
tool allow-listing, credentials, verification, hot updates, and troubleshooting.

## Management API

All management requests require:

```http
Authorization: Bearer <FORGEBOT_ADMIN_TOKEN>
```

Available endpoints:

```text
GET    /v1/admin/config
GET    /v1/admin/config/history
POST   /v1/admin/config/validate
PUT    /v1/admin/config
POST   /v1/admin/config/rollback
GET    /v1/admin/runtime
POST   /v1/admin/runtime/restart
POST   /v1/admin/search/reload
POST   /v1/admin/reflection/scan
GET    /v1/admin/llm/api-key
PUT    /v1/admin/llm/api-key
DELETE /v1/admin/llm/api-key
```

Configuration responses are intentionally redacted. In particular, the LLM
API key is never returned, and OneBot credentials are represented only by
configured/not-configured status.

## Contributing

Contributions are welcome, especially new platform adapters, protocol-compatible
LLM providers, tests, documentation, and operational tooling. Keep new
integrations behind the existing interfaces and preserve the bot core's
protocol-neutral model.

Before opening a pull request:

```powershell
go test ./...
```

## License

No license has been selected for this repository yet. Until a license file is
added, the source should not be assumed to be available for unrestricted
redistribution or commercial use.
