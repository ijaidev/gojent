# AGENTS.md

Root `AGENTS.md` applies; frontend rules live in `frontend/AGENTS.md`. Codjent is a local reference coding agent sample—not a supported product—with no auth, multi-tenant isolation, or deployment story.

## Commands

From `codjent/` (or `just --justfile codjent/justfile <recipe>` from root):
- Test: `just test`
- Run embedded UI / API-only backend: `just run` / `just dev`
- Rebuild UI export / verify committed export: `just frontend` / `just frontend-check` / `just web`

## Where a change goes

Dependency direction is strictly `cmd` → `httpapi` → `app` → `provider`/`store` → `core` (`just arch-check`). Concrete stores and provider catalogs may import `app` types and `app.Store`, never `httpapi` or `cmd`.

| Change | Home |
| --- | --- |
| Process startup, flags, signals | `cmd/codjent` |
| HTTP routing, limits, status codes, SSE | `internal/httpapi`; validate/translate then call `app` |
| Run lifecycle, approvals, thread state | `internal/app` |
| Provider catalog and connection settings | `internal/provider` and `internal/provider/catalog` |
| SQLite persistence | `internal/store/sqlite` |
| Browser UI | `frontend` (see `frontend/AGENTS.md`) |

## Security & invariants

- Bind `127.0.0.1` by default and allow only explicit browser origins; never add wildcard CORS, public bind defaults, or hosted modes without an explicit scope change.
- Keep workspace filesystem/command sandboxing, symlink-safe containment, timeouts, output caps, and request body limits intact. `ask` is the default approval mode (`full` is explicit opt-in); never bypass approval in `ask`.
- Keep provider API keys out of JSON, logs, errors, and UI (`Config.Public`, `api_key_set` only). The existing Google env-key seed is a legacy compatibility exception, not a pattern for new providers; use `providers/internal/httpheader` and the generic catalog.
- Runs are async and per-thread; keep cancellation, approvals, and SSE replay in `internal/app` (never in HTTP handlers or UI).
- Persist canonical `llm.Message` JSON with a format version (no app-only fields inside `llm.Message`); keep SQLite schema creation idempotent, fail clearly if an existing DB cannot migrate, and handle or block deleting referenced providers.
- Test streaming, cancellation, approvals, and persistence deterministically with fakes; update `codjent/README.md` when behavior or scope changes, keeping the sample framing honest.
