# ForgeBot Roadmap

Current implementation planning and handoff are tracked in
[`specs/README.md`](../specs/README.md) and [`specs/roadmap.md`](../specs/roadmap.md).
The phase lists below are historical milestones, not a complete implementation audit.

- [x] Module-level native feature switches and typed Hook chains; first migration:
  reply experience (empty mention, image hint, retrieval progress).
- [ ] External FPP plugin handshake and transports; not part of the native Hook release.

## Phase 1: Foundation

- [x] YAML startup config and JSON runtime config with validation.
- [x] Versioned runtime settings, rollback, and atomic component replacement.
- [x] Runtime-owned Connector lifecycle, status, replacement, and rollback.
- [x] Encrypted write-only LLM secret management.
- [x] Authenticated management API foundation.
- App lifecycle and graceful shutdown.
- HTTP health, runtime, and message ingress.
- Platform-neutral message contracts.
- Sender registry.
- [x] Core Admission plus optional proactive-reply native module.

## Phase 2: Real Platform Adapters

- Telegram adapter.
- [x] Personal QQ / OneBot adapter (HTTP webhook and `send_msg`; runtime
  lifecycle replacement remains a follow-up).
- [x] Conversation-neutral core naming; current bindings remain group-only.
- [x] Protocol-neutral reply relationships and conversation-scoped individual
  administrator identity.
- Shared adapter test harness.
- [x] Webhook authentication and bounded replay protection.

## Phase 3: Local Document Intelligence

- [x] Isolated Markdown, MDX, and plain-text parsing with structured BM25F search.
- [ ] Promote administrator answers into managed Markdown under the optional
  document-root `extra/` directory.
- [ ] Periodically scan a bounded in-memory message window for explicit
  administrator reply pairs; do not persist chat history.
- [x] Configurable document root and bounded indexing budgets.
- [x] Atomic hot reload through operator command and authenticated admin API.
- [x] On-demand LLM tool retrieval and bounded untrusted context builder.
- [ ] Vector and hybrid retrieval remain suspended until document scale
  justifies embedding infrastructure.

## Phase 4: LLM Provider Layer

- [x] Anthropic Messages client.
- [x] OpenAI Responses client.
- [x] OpenAI Completions client.
- [x] Streaming support.
- [ ] Retry policy.
- [x] Timeout policy.
- [x] Provider-independent request/response contract.

The current clients cover request serialization, authentication, normalized
non-streaming responses, complete non-streaming tool-call round trips, and SSE
text deltas. Retry policy and streamed tool-call assembly remain follow-up work.

## Phase 5: Tools and Operations

- [x] Operator-group configuration, command allow-list, and routing contract.
- [x] Runtime-backed status, restart, pause/resume, and proactive commands.
- [x] Non-blocking runtime, Connector, and LLM failure notifications.
- [x] Structured-search reload command, status, and domain notifications.
- [x] MCP registry and allow-listed Streamable HTTP/stdio tool client.
- [ ] Tool execution sandbox policy (external MCP remains network-allowlisted;
  arbitrary shell/filesystem tools are intentionally unsupported).
- [x] Bounded local `SKILL.md` instruction loader.
- Metrics and tracing.
- [x] Minimal admin API surface.
- [ ] Web management frontend.
