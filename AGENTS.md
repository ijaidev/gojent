# AGENTS.md

Gojent is a public Apache-2.0 Go agent runtime with a stdlib-only root module and independent child modules (`codjent`, `providers/*`, `stores/*`, `examples/google`). Scoped rules live in `core/AGENTS.md`, `codjent/AGENTS.md`, and `codjent/frontend/AGENTS.md`; setup, test commands, commits, PRs, dependencies, and releases are in `CONTRIBUTING.md` and `justfile`.

## Where a change goes

| Change | Home |
| --- | --- |
| Agent loop, policies, events, run options | `core/agent` (see `core/AGENTS.md`) |
| Canonical messages, requests, streams, model errors | `core/llm` (see `core/AGENTS.md`) |
| Typed tools, schemas, validation | `core/tool` (see `core/AGENTS.md`) |
| Session identity and `Load`/`Append` contract | `core/session` (see `core/AGENTS.md`) |
| A model provider | `providers/<name>/`; run `llmtest.Run` |
| A session backend | `stores/<name>/`; run `sessiontest.Run` |
| A ready-made tool | `tools/<name>/`; table-driven tests |
| Sample app runs, approvals, persistence | `codjent/internal/app` (see `codjent/AGENTS.md`) |
| HTTP parsing, status codes, SSE | `codjent/internal/httpapi`; calls `app`, never the reverse |
| Provider configuration and catalog | `codjent/internal/provider` |
| Sample UI | `codjent/frontend` (see `codjent/frontend/AGENTS.md`) |
| A runnable example | `examples/<name>/`; must compile and match current APIs |

## Architecture & conventions

- Dependency direction is one-way (`just arch-check`): root (`core/*`, `tools/*`, `providers/mockllm`) is stdlib-only; child modules import `core`, never each other.
- Build providers in `providers/<name>/` using `providers/internal/httpheader`, accurate `llm.Info`, and `llm.Error`/`llm.Kind` classification (never match provider error strings).
- Standard library first (`no testify`); add interfaces only where implementations vary. Public JSON uses `snake_case` and never exposes secrets, internal paths, or vendor SDK types.
- Unit tests must be deterministic and offline (`mockllm`, `httptest`, `t.TempDir`, injected errors; no `sleep` or timing guesses). Run `llmtest.Run` in providers and `sessiontest.Run` in stores.
- Run the narrowest recipe during work and `just check` before handoff on mixed or cross-module changes (skip the frontend build for docs-only edits).
- Keep the public surface small; update `README.md` (preserving the key-free `mockllm` quickstart) and `CHANGELOG.md` (root changes) when public APIs change.

## Boundaries

- **Always:** Treat every tracked file, commit, PR, and comment as public; review `git status` and staged diffs before committing; treat `.agents/skills/**`, `skills-lock.json`, `codjent/frontend/.agents/skills/**`, and `codjent/frontend/skills-lock.json` as vendored (update via skills tooling); keep `AGENTS.md` synced when rules change, and always use `https://github.com/ijaidev/skills/blob/main/skills/writing-agents-md/SKILL.md` before editing this file or any `AGENTS.md` file in this repo.
- **Ask first:** New module, third-party dependency, public interface, provider, or ambiguous scope expansion.
- **Never:** Commit secrets, `.env*` (except `.env.example`), `.local/`, `*.db`, or real prompts/paths; add third-party deps to root `go.mod`; hand-edit `go.work`/`go.work.sum`; weaken cancellation, request limits, containment checks, or test gates.

<!-- os:begin -->
## OpenShip

This repo uses OpenShip for issue/PR workflows. Read `.openship/CONTRACT.md` before changing a spec, ticket, or PR. If it is missing, run `/os-setup`.

<!-- os:version 0.11.0 -->
<!-- os:end -->
