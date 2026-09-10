# 0001: Clean-Room Core

Date: 2026-09-09

## Status

Accepted

## Context

MuseBot contains useful platform integration work, but its core runtime couples
platform adapters, command handling, LLM calls, storage, RAG, MCP, and admin
behavior through shared global configuration.

## Decision

ForgeBot starts with a clean platform-neutral core. Platform adapters convert
external events into `messaging.InboundMessage` and send
`messaging.OutboundMessage`. The bot engine orchestrates policy and routing
through interfaces.

## Consequences

- Telegram and OneBot code can be migrated without inheriting MuseBot's
  `RobotInfo` abstraction.
- LLM providers receive explicit request configuration instead of reading
  global config or storage state.
- Early development is slower than copying MuseBot wholesale, but the result is
  easier to test and evolve.

