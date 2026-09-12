# ForgeBot Architecture

ForgeBot is built around explicit boundaries:

```text
external platform
  -> platform adapter
  -> connector binding (external target -> logical group)
  -> messaging.InboundMessage
  -> bot.Engine
  -> gate.Pool
  -> search / tools / llm
  -> messaging.OutboundMessage
  -> group registry (logical group -> connector target)
  -> connector sender
```

Runtime configuration flows through `settings.Store -> runtime.Manager`. A
generation owns its Engine, LLM client, Retriever, restricted Tool Executor,
Connector Set, and operator routing.
Candidates are built before persistence; only a successful configuration commit
is published. Platform changes stop the old Connector Set, start the candidate,
and restore the old set if startup or persistence fails. Existing Engine
requests finish on their previous generation while new requests use the
replacement.

Operator groups are configured by logical `ConversationID`, never by protocol
or external account identifiers. Their commands pass through a dedicated,
allow-listed operations boundary before ordinary Bot handling. This boundary
must not depend on or expose the complete Web administration service.

## Packages

- `cmd/forgebot`: process entrypoint.
- `internal/app`: composition root and lifecycle.
- `internal/config`: typed configuration loading and validation.
- `internal/secret`: encrypted, write-only-managed secrets with staged activation.
- `internal/settings`: versioned runtime configuration, history, and rollback.
- `internal/runtime`: atomic runtime generations and component lifecycle.
- `internal/admin`: management operations without direct storage access.
- `internal/logging`: slog setup.
- `internal/server`: HTTP health, runtime, and test ingress.
- `internal/messaging`: platform-neutral message contracts.
- `internal/platform`: adapter and sender interfaces.
- `internal/platform/memory`: in-memory/logging sender for local development.
- `internal/platform/onebot`: OneBot 11 HTTP webhook adapter and `send_msg`
  sender, independently constructed behind the platform interfaces.
- `internal/operations`: protocol-neutral operator-group command policy and
  routing contract; command implementations are introduced separately.
- `internal/operator`: restricted runtime operations exposed to operator groups;
  it cannot access secrets or arbitrary administration/configuration methods.
- `internal/notification`: bounded, non-blocking operational event dispatch to
  the currently configured notification-enabled operator groups.
- `internal/bot`: orchestration layer.
- `internal/gate`: group message buffering and deduplication.
- `internal/llm`: protocol-neutral LLM contract and HTTP clients for
  Anthropic Messages, OpenAI Responses, and OpenAI Chat Completions.
- `internal/search`: protocol-neutral retrieval contract and bounded,
  injection-resistant context rendering.
- `internal/search/structured`: MD/MDX parsing, BM25F in-memory indexing,
  deterministic ranking, and atomic index reload.
- `internal/tooling`: explicit tool allow-list and normalized execution result.
- `internal/tooling/searchdocs`: read-only adapter from the tool boundary to a
  configured Retriever.

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
7. Retrieval is on-demand. Normal messages are never prefetched from the
   document index; an explicit `/search` or an LLM `search_docs` call triggers
   retrieval.
8. Retrieved documents are untrusted data, never instructions. Tool execution
   is bounded and allow-listed; it has no implicit Shell, secret, network, or
   configuration access.

## Search Lifecycle

Structured search parses local `.md` and `.mdx` files into heading-aware
sections and builds an immutable in-memory BM25F index. Title, heading,
frontmatter, path, code, and body fields receive distinct weights. Query term
coverage filters weak results, and ties are resolved by stable path and section
identity ordering.

Search configuration belongs to versioned runtime JSON. A configuration update
builds its candidate index before persistence and generation publication. Other
runtime changes and explicit Bot restart reuse the current Retriever. A document
reload builds beside the active immutable index, then atomically swaps the index
snapshot; failed reloads retain the old snapshot and publish a bounded
operational event.

`search.Retriever` is independent of the structured implementation. Vector and
hybrid retrieval may implement the same contract later, but their dependencies,
configuration values, and index files are deliberately absent today.

## LLM Client Contract

`llm.NewClient` selects a protocol client from an explicitly assembled
`llm.ClientConfig`; API key resolution remains in the runtime composition layer.
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
3. Structured search: implemented as an isolated Retriever with no MuseBot
   global configuration or runtime dependencies.
4. LLM protocol clients: implemented behind `internal/llm.Client`, including
   non-streaming tool-call round trips; retry policy and streamed tool-call
   assembly remain follow-up work.

Platform lifecycle contract:

- Adapter.Start returns only after its listener is ready; later failures are
  reported through Adapter.Errors.
- Adapter.Stop is context-bounded and idempotent when the adapter is not running.
- Adapters translate external events into messaging.InboundMessage and do not call the Bot Engine or read configuration files directly.
- OneBot accepts only bound group events and suppresses duplicate event identities
  within a configurable, bounded replay window before invoking the runtime.

The Bot core identifies chats only by logical `ConversationID`. The current
configuration exposes group bindings exclusively, so every active conversation
is currently a group. Protocol names, social accounts, connector IDs, and
external chat IDs remain in the platform layer. A connector can bind multiple
external groups, and multiple connectors of the same protocol can run
simultaneously. Each logical conversation has one outbound binding; changing
its account or protocol does not change the core identity.
