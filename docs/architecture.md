# ForgeBot Architecture

ForgeBot is built around explicit boundaries:

```text
external platform
  -> platform adapter
  -> messaging.InboundMessage
  -> bot.Engine
  -> gate.Pool
  -> search / tools / llm
  -> messaging.OutboundMessage
  -> platform sender
```

## Packages

- `cmd/forgebot`: process entrypoint.
- `internal/app`: composition root and lifecycle.
- `internal/config`: typed configuration loading and validation.
- `internal/logging`: slog setup.
- `internal/server`: HTTP health, runtime, and test ingress.
- `internal/messaging`: platform-neutral message contracts.
- `internal/platform`: adapter and sender interfaces.
- `internal/platform/memory`: in-memory/logging sender for local development.
- `internal/bot`: orchestration layer.
- `internal/gate`: group message buffering and deduplication.
- `internal/llm`: protocol-neutral LLM contract and HTTP clients for
  Anthropic Messages, OpenAI Responses, and OpenAI Chat Completions.

## Design Rules

1. Platform adapters do not call LLMs or write storage directly.
2. LLM providers do not read global config or write records.
3. Gate policy is injected and can be refreshed without importing config.
4. Storage, retrieval, tools, and metrics are middleware around the engine, not
   hidden side effects inside providers.
5. New platforms should not require edits to the core engine.
6. The LLM boundary supports exactly three wire protocols:
   `anthropic_messages`, `openai_responses`, and `openai_completions`.
   Vendors are configuration targets, not new protocol abstractions.

## LLM Client Contract

`llm.NewClient` selects a protocol client from `config.LLMConfig.ClientConfig`.
All three clients expose the same two operations:

- `Complete`: JSON request/response with normalized text, finish reason, usage,
  and tool calls.
- `Stream`: server-sent events normalized into text deltas, tool-call deltas,
  completion metadata, and terminal errors.

`openai_completions` intentionally means the mainstream Chat Completions
contract at `/chat/completions`; legacy text completions are not part of the
ForgeBot protocol surface.

## First Migration Targets

1. Telegram adapter: reuse MuseBot SDK setup, message parsing, media send, and
   streaming edit behavior.
2. Personal QQ / OneBot adapter: reuse DTOs, HMAC-SHA1 verification, segment
   parsing, reply segment construction, and HTTP send API.
3. Structured search: migrate MuseBot `structuredsearch` as `internal/search`.
4. LLM protocol clients: implemented behind `internal/llm.Client`; retry
   policy and richer tool-call streaming are follow-up work.
