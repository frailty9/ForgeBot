# ForgeBot Roadmap

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
- Group message gate.

## Phase 2: Real Platform Adapters

- Telegram adapter.
- [x] Personal QQ / OneBot adapter (HTTP webhook and `send_msg`; runtime
  lifecycle replacement remains a follow-up).
- [x] Conversation-neutral core naming; current bindings remain group-only.
- Shared adapter test harness.
- Webhook authentication and replay protection.

## Phase 3: Local Document Intelligence

- Migrate MD/MDX structured search.
- Configurable document root.
- Hot reload and webhook reload.
- Context builder for LLM prompts.

## Phase 4: LLM Provider Layer

- [x] Anthropic Messages client.
- [x] OpenAI Responses client.
- [x] OpenAI Completions client.
- [x] Streaming support.
- [ ] Retry policy.
- [x] Timeout policy.
- [x] Provider-independent request/response contract.

The current clients cover request serialization, authentication, normalized
non-streaming responses, and SSE text deltas. Retry policy and richer streamed
tool-call assembly remain follow-up work.

## Phase 5: Tools and Operations

- [x] Operator-group configuration, command allow-list, and routing contract.
- [x] Runtime-backed status, restart, pause/resume, and Gate commands.
- [x] Non-blocking runtime, Connector, and LLM failure notifications.
- [ ] Structured-search reload command and richer domain notifications.
- MCP registry.
- Tool execution sandbox policy.
- Metrics and tracing.
- [x] Minimal admin API surface.
- [ ] Web management frontend.
