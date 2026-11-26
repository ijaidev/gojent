# AGENTS.md

Root `AGENTS.md` applies. This file covers `core/agent`, `core/llm`, `core/tool`, and `core/session`.

## Boundaries

- `core` imports only Go stdlib and `core/*` (`just arch-check`). Keep vendor SDKs, workflow engines, graph DSLs, vector DBs, and UI out of core.
- Keep messages and contracts provider-neutral: carry vendor state in provider-data/replay fields and `llm.Info.Features`, never new core fields.
- Do not log in `core`—return `package:`-prefixed errors or emit events.

## Contracts & invariants

- **`agent.Agent`:** Immutable and safe for concurrent runs; never store per-run mutable state on `Agent` or globals, and never block unrelated runs on a shared slot.
- **Option precedence:** Package defaults `< agent.Config < agent.RunOptions`. `Budget` and `RetryPolicy` merge field-by-field; `Session` is per-run. Preserve these rules in `ApplyAgentOptions`.
- **Fail closed:** Unrecognized policy decisions deny; `Ask` without an approver denies; reject unhonorable requests with `llm.ErrUnsupported` instead of silently dropping options; keep `llm.Info` accurate.
- **`session.Store`:** Exactly `Load` and atomic `Append` (assistant tool-call message + all tool results in one call). Return messages chronologically, isolate `ResourceID`, and deep-copy reused buffers.
- **`tool.Tool`:** Validate inputs against generated JSON Schema before `Execute` (invalid args return as tool errors); contain panics, timeouts, and cancellation; pair every `ToolStart` with one `ToolEnd`; keep `Annotations` honest.
- **`llm.Message`:** Canonical sealed parts with a format version; unknown part types fail closed and must not break persisted transcripts.
- **Extensibility:** Public seams are `llm.Model`/`llm.Streamer`, `tool.Tool`, `session.Store`, and `agent.Policy`/`Approver`/`Sink`. Wrap a seam instead of adding hooks or speculative interfaces.
