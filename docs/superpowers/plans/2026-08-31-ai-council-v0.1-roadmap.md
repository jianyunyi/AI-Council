# AI Council v0.1 Implementation Roadmap

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a local-first Web application in which OpenAI, Anthropic, and DeepSeek independently propose and review code changes, while a hash-bound human approval gate controls all file writes and commands.

**Architecture:** A Go monorepo contains a go-zero Council Server and a Gin Workspace Runner connected by gRPC. A Next.js/React console uses REST and resumable SSE. Work is split into four plans so every phase produces independently testable software.

**Tech Stack:** Go 1.25+, go-zero, Gin, GORM, gRPC/protobuf, SQLite, React, Next.js, TypeScript, pnpm, Vitest, Playwright

---

Execute the plans in this order:

1. [Core foundation and state machine](2026-08-31-ai-council-v0.1-01-core.md)
2. [Providers and Council protocol](2026-08-31-ai-council-v0.1-02-council.md)
3. [Approval-bound Workspace Runner](2026-08-31-ai-council-v0.1-03-runner.md)
4. [Web console and end-to-end delivery](2026-08-31-ai-council-v0.1-04-web-e2e.md)

Each phase ends with a repository-wide verification and a commit. Do not begin a later phase while the previous phase has failing tests.

## Specification coverage

- Product boundaries, Go architecture, state transitions, artifacts, audit storage, and both process shells: Phase 1.
- Ephemeral keys, all three providers, provider usage records, independent proposals, blind review, Judge, red team, quorum, budget, and execution-plan generation: Phase 2.
- Runner pairing, path and sensitive-file policy, immutable approval hashes, user-file protection, direct argv execution, idempotency, deterministic verification, and recovery evidence: Phase 3.
- Task orchestration, approval persistence, one replan, REST/SSE, all Web pages, reconnect behavior, setup flows, approval UI, verification reports, sample repositories, E2E security cases, and operator documentation: Phase 4.

The implementation environment currently provides Go 1.26.3, Node 22.14.0, pnpm 11.19.0, goctl 1.10.2, and protoc 29.3. The global npm command is broken, so every JavaScript step uses pnpm. The module remains compatible with the design minimum of Go 1.25.
