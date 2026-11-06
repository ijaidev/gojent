# AGENTS.md

Gojent is a public, Apache-2.0 Go project. The root module is a small, provider-neutral agent runtime. `codjent` is a sample coding agent built on it; `codjent`, `providers/*`, `stores/*`, and `examples/google` are independent modules. These rules apply repository-wide. Codjent-specific rules are in `codjent/AGENTS.md`; frontend and UX rules for codjent are in `codjent/frontend/AGENTS.md`. Workspace setup, dependency changes, releases, commits, and pull requests are covered in `CONTRIBUTING.md`.

## Commands

- Create the local workspace once: `just workspace`
- Run all race-enabled unit tests: `just test`
- Run the full gate: `just check`
- Run one module's checks: `just --justfile providers/google/justfile test`
- Run one test: `go test -race ./core/agent -run TestAgent_Generate`
- Rebuild and verify the codjent UI: `just --justfile codjent/justfile frontend-check`

## Security and public content

- This is a public repository: assume every tracked file, diff, commit message, issue, pull request, and review comment is public. Never commit API keys, `.env*` files (except `.env.example` with placeholders), tokens, private URLs, customer data, or real production data. Review `git status` and the staged diff before committing, and never force-add ignored files. If private data appears, stop and alert the maintainer.
- Keep local runtime data local. `.local/`, `*.db`, logs, attachments, and generated provider settings are ignored on purpose; never add fixtures containing real prompts, workspace paths, or credentials. Use `.env.example` for variable names and never print secret values.
- Treat provider credentials, filesystem access, command execution, and HTTP boundaries as security-sensitive. Never log secrets or weaken cancellation, request limits, or containment checks.
- Unit tests must be deterministic and offline: use `mockllm`, `httptest`, `t.TempDir`, and injected errors. Never call an external provider or network service or require an API key. The PostgreSQL integration suite is the only opt-in external test and requires `GOJENT_POSTGRES_DSN`.

## Architecture and boundaries

- The root module is deliberately small and standard-library only. The four core packages are `core/agent` (immutable agents, runs, policies, events), `core/llm` (provider-neutral messages, requests, streams, errors), `core/tool` (typed tools, schemas, validation), and `core/session` (identity and the two-method store contract). Do not add a third-party dependency to the root `go.mod`; put vendor-specific code in an adapter module.
- Public seams are `llm.Model`/`llm.Streamer`, `tool.Tool`, `session.Store`, and `agent.Policy`/`Approver`/`Sink`. The execution loop is private, and `Generate`/`Stream` are the public entry points. Add middleware by wrapping a seam, not by adding hooks or speculative interfaces. Add an interface only where something really varies across it.
- Keep the dependency direction one-way: `codjent`, providers, stores, and examples import the core; the core never imports them. Optional modules stay optional. `tools` and `providers/mockllm` live in the root module but must also remain standard-library only.
- Keep the core provider-neutral. Canonical messages and parts carry vendor state through provider data/replay fields, not through new core fields. Vendor SDK types, vendor configuration, and protocol translation belong in adapters and composition roots. Do not add a new provider-specific field to a shared contract when provider data or `llm.Info.Features` can express it.
- Add a provider through a new adapter module with accurate `Info`, canonical error mapping, and deterministic conformance coverage. Reuse shared adapter helpers such as `providers/internal/httpheader`, and keep provider configuration and protocol translation in the adapter.
- No workflow engine, graph DSL, vector database, UI, or hosted service belongs in the core. Composition is ordinary Go: functions, goroutines, and `errgroup`-style code.
- Application-specific behavior, including codjent's local-only scope and layering, belongs in its own module and its own `AGENTS.md`; it must not leak into core contracts.

## Core contracts and invariants

- An `agent.Agent` is immutable, reusable behavior. A run cannot mutate it; per-run state lives in `agent.RunOptions`. Precedence is package defaults, then `agent.Config`, then `RunOptions`. `Budget` and `RetryPolicy` merge field by field; `Session` is always per-run. Preserve these merge rules in `ApplyAgentOptions`.
- The same agent may serve many concurrent runs. Never store per-run mutable state on `Agent` or in package globals, and never make unrelated runs wait on a shared slot when only one resource needs limiting. Coordinate shared work through explicit dependencies and cancellation.
- Fail closed. An unrecognized policy decision is deny; `Ask` without an approver is deny; providers reject requests they cannot honor with `llm.ErrUnsupported` instead of silently dropping options. Model `Info` must be accurate.
- Persistence is atomic. `session.Store` has exactly `Load` and `Append`; `Append` persists the assistant tool-call message and all its tool results in one call, so a crash cannot leave unanswered calls. Return messages chronologically, isolate `ResourceID`, and deep-copy when a backend reuses buffers.
- Tools are typed functions plus metadata. The generated JSON Schema and validation run before the handler; invalid arguments go back to the model as a tool error. Panics, timeouts, and cancellation are contained by the runtime. Keep `Annotations` honest and preserve event pairing (`ToolStart`/`ToolEnd`).
- Messages are canonical sealed parts with a format version. Unknown part types fail closed. New provider behavior must round-trip through the existing provider-data/replay model and must not break persisted transcripts.
- Keep the public surface small. Public API changes need tests, package documentation, and a corresponding README update. Pre-v1 minor versions may break, but break deliberately and explain the reason.

## Go conventions

- Format with `gofmt -s` (`just fmt`); run `just lint` (golangci-lint) and `just vet`. Standard library first: no testify, and no new dependency unless the standard library or an existing module cannot reasonably do the job.
- Write doc comments for exported API and comments that explain intent, invariants, or tradeoffs, not restate the code; `just lint` enforces the mechanical rules.
- Use sentinel errors, `fmt.Errorf("...: %w", err)` wrapping, and `errors.Is`/`errors.As` for inspection, matching the surrounding package's existing style (core packages use a `package:` prefix). Provider adapters must classify failures with `llm.Error` and `llm.Kind`; never match provider error strings. Do not log in the core—return the error or emit an event.
- Constructors validate required configuration and return errors. Use functional options for optional configuration when the type already follows that pattern. `Must` constructors are only for failures that are programming mistakes at startup.
- Accept `context.Context` as the first parameter, honor cancellation and deadlines, and give every goroutine a clear exit. Close resources on every path; do not start background work without an owner that can stop it.
- Prefer the existing primitives and one source of truth. Use `slices`, `maps`, and `cmp` where they simplify code. Avoid speculative abstractions, exported helpers that only one call site needs, and formatting churn mixed with behavior changes.
- Public JSON uses snake_case tags. Do not expose secrets, internal paths, or provider SDK types through public structs.

## Tests and verification

- CI runs the same recipes as `just check`. Before handoff, run the narrowest recipe that covers the change; run `just check` for mixed, risky, or cross-module changes. A docs-only change does not need the full frontend build.
- Add tests for every behavior change and every bug fix. Table-driven tests with `t.Run` are the norm. Use an external `_test` package when exercising the public API and an internal test package when the behavior depends on unexported code.
- Every provider adapter must run `llmtest.Run`; every session store must run `sessiontest.Run`. Keep the conformance suites deterministic and provider-neutral. Add adapter-specific tests only for behavior the shared suite cannot express.
- Do not use sleeps or timing guesses to synchronize tests. Use contexts, channels, `httptest`, and injected fakes. Do not weaken assertions or skip tests to make a suite pass.
- The PostgreSQL store's integration and conformance tests are opt-in through `GOJENT_POSTGRES_DSN`; unit tests must still pass without a database.

## Documentation and public API

- The README is the public contract and the first thing users see. Update it when public APIs, commands, provider support, repository layout, or versioning change. Keep `CHANGELOG.md` current for user-visible core changes. Keep the quickstart runnable with no API key by using `providers/mockllm`.
- Every example must compile in its module and match the API it demonstrates. If you change an API used by an example, update the example and its test.
- Package documentation states the package's responsibility and its invariants. Keep docs close to the code they describe; do not duplicate large API explanations in several places.
- Do not add marketing claims, benchmarks, or roadmap promises that the code does not support.

## Agent workflow

- For Go work, start with `.agents/skills/golang-how-to` and load the relevant focused skills (for example `golang-testing`, `golang-error-handling`, `golang-security`, `golang-documentation`, or `golang-continuous-integration`). For interface or module design, use `.agents/skills/codebase-design`.
- Treat `.agents/skills/**`, `skills-lock.json`, `codjent/frontend/.agents/skills/**`, and `codjent/frontend/skills-lock.json` as vendored. Do not hand-edit skill content or hashes; update them through the project's skills tooling.
- Before committing, opening a PR, adding a module or dependency, or cutting a release, follow `CONTRIBUTING.md`.
- Read the surrounding code before editing. Match its naming, error style, test layout, and dependency choices; do not reformat untouched files or rename things outside the task.
- Keep each rule at the narrowest scope. Nested `AGENTS.md` files add to the root rather than repeat it, and these files are maintained by hand; update stale or contradictory rules with the code change.
- Do not hand-edit generated files. Regenerate `codjent/frontend/out` with the codjent recipe and commit the result; `go.work`/`go.work.sum` are local, and `next-env.d.ts` is managed by Next.
- If a task requires a new module, dependency, public interface, provider, or broad UI pattern, state the reason in the change, follow `CONTRIBUTING.md` for workspace and release mechanics, and keep it within the boundaries above. When the correct scope is ambiguous, ask before expanding it.
