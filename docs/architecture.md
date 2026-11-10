# ForgeBot Architecture

ForgeBot is built around explicit boundaries:

```text
external platform
  -> native connector provider module
  -> platform adapter
  -> connector binding (external target -> logical group)
  -> messaging.InboundMessage
  -> core Admission (validation and deduplication)
  -> native module observers / participants / commands
  -> bot.Engine
  -> bounded agent loop / tools / llm
  -> messaging.OutboundMessage
  -> group registry (logical group -> connector target)
  -> connector sender
```

Runtime configuration flows through `settings.Store -> runtime.Manager`. A
generation owns its Engine, LLM client, native Module Set, restricted Tool Executor,
Connector Set, and operator routing.
Candidates are built before persistence; only a successful configuration commit
is published. Platform or connector-Provider changes stop the old Connector
Set, start the candidate, and restore the old set if startup or persistence
fails. Existing Engine
requests finish on their previous generation while new requests use the
replacement.

The process selects one persistence Backend and obtains domain-specific
configuration, credential and group-operation repositories. Short-term
conversation memory and proactive yielding belong to the process Runtime,
not an individual generation. A generation leases those services without
closing them when its own Engine retires.

Group requests and member events are separate from chat messages. Optional
connector operations include join review and message recall; native event
modules return restricted reply intents for the current event. The host owns
delivery, authorization, durable idempotency and execution certainty.

The host provides `group.message_recall.v1` to modules as a narrow
RecallProvider. It binds a logical conversation before exposing
`Recall(ctx, messageID)`. Execution rechecks the binding and pause state and
routes to the optional connector Recaller; it does not depend on history,
message age, platform chat roles, a slash command, or a policy model. No caller
strategy, LLM tool, HTTP action or history mutation is installed automatically.

Join-review modules contribute data-only policies. The Core group service
combines regex, optional external HTTP review, and the shared decision model;
external requests resolve only configured Header secret references through the
host and enforce a 5-second deadline and 64KiB response limit. JSON templates
replace decoded string values and are serialized afterwards. Policies are
compared by effective content, including external headers, before execution.

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
- `internal/agent`: bounded multi-round answer execution and tool budgets.
- `internal/conversation`: process-local, isolated short-term question/answer context.
- `internal/decision`: task schemas, isolated LLM decisions and optional Jev choices.
- `internal/operation`: stable failure categories and independent effect certainty.
- `internal/persistence`: one backend selection, providing domain repositories.
- `internal/group`: member/request events, optional operations and durable execution states.
- `internal/catalog`: read-only operator-facing capability projection.
- `internal/modules/welcome`: configurable member welcome templates.
- `internal/admin`: management operations without direct storage access.
- `internal/logging`: slog setup.
- `internal/server`: HTTP health, runtime, and test ingress.
- `internal/messaging`: platform-neutral message contracts.
- `internal/platform`: adapter and sender interfaces.
- `internal/platform/provider.go`: protocol-neutral Connector Provider contract,
  filtered binding input, and adapter build request.
- `internal/platform/memory`: in-memory/logging sender for local development.
- `internal/modules/echo`: native `echo` Provider module for the in-memory
  development adapter.
- `internal/modules/onebot`: native `onebot` Provider module; owns OneBot
  options, defaults, redaction, binding conversion, and adapter construction.
- `internal/platform/onebot`: OneBot 11 HTTP webhook adapter and `send_msg`
  sender, without RuntimeConfig knowledge.
- `internal/operations`: protocol-neutral operator-group command policy and
  routing contract; command implementations are introduced separately.
- `internal/operator`: restricted runtime operations exposed to operator groups;
  it cannot access secrets or arbitrary administration/configuration methods.
- `internal/administration`: conversation-scoped administrator identity
  resolution, independent from platform protocols and operator-group routing.
- `internal/notification`: bounded, non-blocking operational event dispatch to
  the currently configured notification-enabled operator groups.
- `internal/bot`: orchestration layer.
- `internal/admission`: non-optional inbound deduplication owned by the core.
- `internal/proactive`: bounded delayed participation used by the optional
  `native.proactive_reply` module, including trusted-administrator cancellation.
- `internal/llm`: protocol-neutral LLM contract and HTTP clients for
  Anthropic Messages, OpenAI Responses, and OpenAI Chat Completions.
- `internal/search`: protocol-neutral retrieval contract and bounded,
  injection-resistant context rendering.
- `internal/search/structured`: Markdown/MDX/plain-text parsing, BM25F in-memory indexing,
  deterministic ranking, and atomic index reload.
- `internal/tooling`: explicit tool allow-list and normalized execution result.
- `internal/tooling/searchdocs`: read-only adapter from the tool boundary to a
  configured tool providers.
- `internal/tooling/mcp`: configured MCP client with transport abstraction.
  Streamable HTTP and stdio subprocess transports discover only allow-listed
  tools and expose them under the `mcp.<server>.<tool>` namespace.
- `internal/skills`: bounded local `SKILL.md` loader. Skill files contribute
  instructions only; they cannot execute code or select MCP endpoints.

## Design Rules

1. Platform adapters do not call LLMs or write storage directly.
2. LLM providers do not read global config or write records.
3. Core Admission is always active; optional proactive buffering is injected as a
   native module and can be refreshed without importing config.
4. Storage, retrieval, tools, and metrics are middleware around the engine, not
   hidden side effects inside providers.
5. New protocols should not require edits to the core engine: a Provider
   validates opaque options, produces a redacted public view, and builds an
   unstarted Adapter. `Adapter.Start` remains ConnectorSet-owned.
   Runtime discovers Providers exclusively through enabled module registrations;
   lifecycle tests use the same module/Provider/ConnectorSet path. Builder no
   longer exposes ConnectorFactory or host ConnectorProviders injection.
6. The LLM boundary supports exactly three wire protocols:
   `anthropic_messages`, `openai_responses`, and `openai_completions`.
   Vendors are configuration targets, not new protocol abstractions.
7. Retrieval is on-demand through the tool executor. An LLM `search_docs` call
   uses the same authorization and budget boundaries as other tools.
8. Retrieved documents are untrusted data, never instructions. Tool execution
   is bounded and allow-listed; it has no implicit Shell, secret, network, or
   configuration access.
9. Platform adapters normalize trusted owner/admin roles for human collaboration.
   Configured ForgeBot administrator identities retain their separate source;
   platform roles never automatically grant credential or configuration authority.

## Declarative Native Modules

The composition root explicitly registers factories in `internal/modules`.
`runtime.modules` enables or disables each whole module, not individual hooks.
Factories validate their own options; configuration storage continues to use
`settings.Store` with `JSONFileStore`. Runtime snapshots and history own their
maps, slices, pointers and opaque module options rather than sharing mutable data.

`internal/hooks` compiles immutable typed chains for `agent.input.prepare`,
`tool.before_execute`, and `reply.before_send`. Inputs contain value-only text
and attribution, never mutable identity, media, routing targets, credentials or
the Runtime Manager. Typed results are validated before application. Source tier,
host order, module ID and handler ID determine order. Business equivalence or
replacement coverage is deliberately not checked.

The first native module is `native.reply_experience`: empty mention and image
analysis hints plus a once-per-run retrieval progress effect. The host sends
effects only after the tool chain passes. No message-sending tool is added to the
LLM. Reply hooks cover Engine replies only, not operator/notification delivery.
`native.proactive_reply` now owns unaddressed-message buffering through the
generic participant registration and a constrained deferred responder. Reflection,
search Tool registration, command registration, and async jobs remain in migration.

The Engine owns module cleanup after construction; failed builders clean up
candidate modules. Retirement waits for direct requests and active participant callbacks
before closing modules and tools. Pending proactive items retain the existing shutdown
cancel/drop semantics; they are not persisted or replayed. One overdue retirement
blocks further generation rebuilds with `runtime_busy`. This is not a forced
unload or rollback of effects already delivered to external systems.

Native handlers execute synchronously with cooperative cancellation (200 ms per
handler, 1 s per node chain). No goroutine is spawned per hook to fake a hard
timeout. Uncooperative native code cannot be forcibly interrupted. The factory
and cleanup contract is likewise for trusted native code, not a sandbox.
External-source test doubles exercise ordering, but no FPP transport, public SDK,
plugin installer, background-job API or asynchronous observation queue exists yet.

## Search Lifecycle

Structured search parses local `.md`, `.mdx`, and `.txt` files and builds an
immutable in-memory BM25F index. Markdown inputs produce heading-aware sections;
plain-text inputs are bounded without interpreting Markdown syntax. Title, heading,
frontmatter, path, code, and body fields receive distinct weights. Query term
coverage filters weak results, and ties are resolved by stable path and section
identity ordering.

`native.structured_search` owns the index and its runtime options. A configuration
update builds the candidate module before persistence and generation publication.
The module registers `search_docs` into the same LLM Tool directory used by MCP
tools and provides typed reload/status
capabilities. A document reload atomically swaps the index snapshot; failures
retain the old snapshot and publish a bounded operational event.

The `search.Retriever` contract remains implementation-neutral behind the native
Tool module. Vector and hybrid retrieval may implement the same contract later,
but their dependencies, configuration values, and index files are deliberately
absent today.

The reflection pipeline obtains a bounded PublisherFactory capability from the
active search module. Relative extra paths resolve from the module document root;
absolute paths are used as-is. Search itself remains read-only and indexes the
published directory through its normal recursive scan.

`native.knowledge_reflection` registers only a history Appended subscription,
a periodic Background Job, status, and `reflection.control`. It requires the
host history Reader, LLM completer, search control, publisher factory, and logical
conversation catalog. Administrator reply anchors resolve questions from the
same conversation's history or the adapter's already resolved quoted text.
Only anchored sessions retain bounded question/answer snapshots and processing
marks; there is no parallel candidate pool for ordinary member messages.
Snapshots survive normal history eviction and expiry without subscribing to
Evicted or pinning records. The module owns quiet-window batching, single-flight
processing, capacity, expiry and cancellation. Candidate construction prepares
these resources without starting the job or creating the output directory;
activation and retirement are owned by ModuleSet. The pipeline consumes the
pure `reflection/session.AnswerSession` contract; reflection and Markdown
publication algorithms are unchanged.

## MCP Transport Rules

- `streamable_http` uses an explicitly configured HTTP(S) endpoint.
- `stdio` starts the configured executable directly with `exec.Command`; no
  shell interpolation is performed. stdout is reserved for newline-delimited
  JSON-RPC, while stderr is isolated from the protocol stream.
- A stdio timeout terminates the child process and invalidates the candidate
  runtime generation. The previous generation remains active during a failed
  replacement.

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
3. Structured search: implemented as a native Tool module with an isolated
   Retriever implementation and no platform-specific dependencies.
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

### Connector Provider lifecycle

`modules.native.<provider>` enables an implementation such as `native.onebot`.
`platforms.connectors[]` selects it by `type` and carries opaque instance
`options`; `platforms.groups[]` remains the protocol-neutral mapping from an
external target to a logical conversation. During generation preparation,
ForgeBot builds the Module Set first, reads its Provider catalog, builds an
unstarted ConnectorSet, and then constructs the Engine.

Provider module enablement or options changes are classified as
`restart_connectors`: the old ConnectorSet stops, the candidate set starts, and
the old generation retires only after publication. Non-provider module changes
remain `replace_modules` updates and reuse the active ConnectorSet. Management
validation and public configuration rendering build only Provider modules, so
protocol options are validated and redacted without starting a listener,
building an index, or starting background work.
