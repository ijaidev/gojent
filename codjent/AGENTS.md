# AGENTS.md

Codjent is the sample coding agent built on Gojent: a Go backend, an embedded static Next.js UI, SQLite persistence, streaming runs, tools, and approvals. The root `AGENTS.md` applies. This file adds codjent-specific rules; frontend and UX rules live in `frontend/AGENTS.md`.

## Scope and security

- Codjent is a reference sample, not a supported product. Its scope is deliberately small and it has no authentication, multi-tenant isolation, or deployment story. Do not add those quietly or imply that it is safe to expose publicly.
- Keep the default listener on `127.0.0.1` and allow only explicit browser origins. Do not add wildcard CORS, a public bind default, or a hosted mode without an explicit scope decision.
- Filesystem and command tools are sandboxed to the configured workspace. Preserve symlink-safe containment, command timeouts and output caps, and request body limits. `ask` is the safe default; `full` is an explicit opt-in. Never bypass approval in `ask` mode.
- Provider API keys are secrets. Keep them out of JSON, logs, errors, and UI: use `Config.Public`, expose only `api_key_set`, and never add a route or debug path that returns the stored key. The existing Google environment-key seed is a compatibility exception, not a pattern for new providers.
- Treat HTTP parsing, CORS, security headers, and provider credentials as security-sensitive. Keep local databases, logs, attachments, and `.local/` data out of commits.

## Architecture and providers

- Backend layers are `cmd` → `httpapi` → `app` → `provider`/`store` → core. Handlers validate and translate; the app owns runs, approvals, and persistence; provider translation stays out of HTTP handlers. Browser boundaries are in `frontend/AGENTS.md`.
- Add a provider through a new adapter module plus the shared provider format, catalog, and connection settings. Reuse `providers/internal/httpheader` for static and dynamic headers. Do not add a provider-specific environment variable, route, or database column when the generic catalog can express the behavior; the Google seed is a legacy path.
- Runs are asynchronous and per-thread; keep cancellation, approval, and SSE replay consistent. Do not put run orchestration in HTTP handlers or the UI.
- The frontend is a static export embedded in the binary; `frontend/out` is committed. Use the codjent recipes to rebuild it and never hand-edit it.

## Where a change goes

| Change | Home |
| --- | --- |
| Run lifecycle, approvals, persistence, thread state | `internal/app` |
| HTTP routing, request limits, status codes, SSE | `internal/httpapi`; validate and translate, then call `app` |
| Provider catalog and connection settings | `internal/provider` and `internal/provider/catalog` |
| SQLite persistence | `internal/store/sqlite` |
| Process startup, flags, signal handling | `cmd/codjent` |
| Browser UI | `frontend`, following `frontend/AGENTS.md` |

The dependency direction is `cmd` → `httpapi` → `app` → `provider`/`store` → core.
`app` never imports `httpapi` or `cmd`, and `httpapi` never imports `cmd`.
Concrete stores and provider catalogs may import `app` types and the
`app.Store` surface they implement; they must not reach upward into `httpapi`
or `cmd`. Run `just arch-check` from the repository root after moving code
across these layers.

## Persistence and compatibility

- Codjent's SQLite store persists canonical `llm.Message` JSON with a format version. Keep schema creation idempotent and preserve existing local databases; if a layout cannot be migrated, fail with a clear message rather than silently rewriting transcripts.
- Keep provider configuration and thread references consistent: deleting a provider that threads use must remain blocked or handled explicitly.
- Do not put app-only persistence fields into `llm.Message`; use the canonical payload plus the app envelope and derived views.

## Build and verification

- Run `just --justfile codjent/justfile test` for the module. From the repository root, the same justfile provides `run` for the embedded UI, `dev` for API-only frontend development, `frontend` to rebuild the export, `frontend-check` to verify it is current, and `web` to build and run the embedded UI.
- After frontend changes, run `frontend-check` and commit `frontend/out`. Frontend lint, test, and type-check commands are in `frontend/AGENTS.md`.
- Unit tests must stay deterministic and offline. Use `mockllm`, `httptest`, and `t.TempDir`; do not call a real provider or require a key. Exercise streaming, cancellation, approvals, and persistence with fakes.
- `codjent` is its own module. Keep its `go.mod` and `justfile` tidy and use the root `just` recipes for cross-module checks.

## Documentation

- `codjent/README.md` describes the sample's current scope, commands, and configuration. Update it when behavior or scope changes, and keep the "sample, not a supported product" framing honest.
