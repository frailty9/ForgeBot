# ForgeBot Roadmap

## Phase 1: Foundation

- Typed config with validation and env overrides.
- App lifecycle and graceful shutdown.
- HTTP health, runtime, and message ingress.
- Platform-neutral message contracts.
- Sender registry.
- Group message gate.

## Phase 2: Real Platform Adapters

- Telegram adapter.
- Personal QQ / OneBot adapter.
- Shared adapter test harness.
- Webhook authentication and replay protection.

## Phase 3: Local Document Intelligence

- Migrate MD/MDX structured search.
- Configurable document root.
- Hot reload and webhook reload.
- Context builder for LLM prompts.

## Phase 4: LLM Provider Layer (in progress)

- [x] Anthropic Messages client.
- [x] OpenAI Responses client.
- [x] OpenAI Completions client.
- [x] Streaming support.
- Retry and timeout policy.
- [x] Provider-independent request/response contract.

The current clients cover request serialization, authentication, normalized
non-streaming responses, and SSE text deltas. Retry policy and richer streamed
tool-call assembly remain follow-up work.

## Phase 5: Tools and Operations

- MCP registry.
- Tool execution sandbox policy.
- Metrics and tracing.
- Minimal admin surface.
