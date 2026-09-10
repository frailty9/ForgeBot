# ForgeBot

ForgeBot is a clean-room multi-platform bot foundation for ForgeBot.

The project starts from a small, testable core instead of inheriting the
historical MuseBot runtime. Platform adapters convert external events into a
single message model; the bot engine applies admission control, routing,
retrieval, tools, and LLM providers through explicit interfaces.

## Current Scope

- Typed JSON configuration with defaults, validation, and environment overrides.
- Structured runtime lifecycle with graceful shutdown.
- HTTP management and test ingress.
- Unified inbound/outbound message contracts.
- Platform sender registry.
- Reusable group message gate with buffering, deduplication, and stats.
- A deliberately small LLM boundary supporting Anthropic Messages, OpenAI
  Responses, and OpenAI Chat Completions, including normalized JSON and SSE
  clients.

Real Telegram and OneBot adapters are intentionally not wired in this first
slice. Their protocols should be migrated after the internal boundaries are
stable.

## Quick Start

```powershell
go test ./...
go run ./cmd/forgebot -config configs/forgebot.example.json
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

Send a test message through the HTTP ingress:

```powershell
Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:8080/v1/messages `
  -ContentType application/json `
  -Body '{"platform":"echo","chat_id":"local","user_id":"u1","message_id":"m1","text":"hello ForgeBot"}'
```

## Architecture

See [docs/architecture.md](docs/architecture.md).
