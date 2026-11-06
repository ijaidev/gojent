# Contributing to Gojent

Gojent is a public Apache-2.0 project. The root module is a small, standard-library-only agent runtime; `providers/*`, `stores/*`, `codjent`, and `examples/google` are independent modules. Read `AGENTS.md` before changing code; this file covers the contribution workflow.

## Development setup

- Go 1.25.5 or newer, `just` 1.58 or newer, and Node.js 24 with pnpm 11.0.8 for the codjent frontend.
- Run `just workspace` once after cloning. `go.work` and `go.work.sum` are local files and are intentionally untracked.
- Each module owns its `go.mod`, `go.sum`, and `justfile`. Add a new module to the `modules` list in the root `justfile` and give it the standard `test`, `vet`, `build`, `lint`, `tidy`, and `tidy-check` recipes.
- The root `justfile` delegates to every module. Run one module's recipe with `just --justfile <dir>/justfile <recipe>`.

## Checks

- `just check` is the full local gate: formatting, tidy, vet, build, lint, race-enabled tests, repository script tests, and the embedded-frontend check. CI runs the same recipes.
- For a focused change, run the narrowest recipe that covers it, then the full gate before handoff. A docs-only change does not need the frontend build.
- PostgreSQL integration and conformance tests are opt-in:

```sh
GOJENT_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/gojent_test?sslmode=disable' \
  just --justfile stores/postgres/justfile test-integration
```

- Frontend checks run from `codjent/frontend`: `pnpm install --frozen-lockfile`, `pnpm lint`, `pnpm test`, `pnpm exec tsc --noEmit`, and `pnpm build`. After UI changes, run `just --justfile codjent/justfile frontend` and commit `frontend/out`.
- Unit tests must stay deterministic and offline. Use `mockllm`, `httptest`, and `t.TempDir`; never call a real provider or require an API key.

## Dependencies

- Keep the dependency direction one-way: child modules may import the root module, but the root never imports a child module, and unrelated child modules should not import each other.
- The root module is standard-library only. Do not add a third-party dependency to the root `go.mod`; put vendor-specific code in an adapter module.
- Treat a new dependency as a design decision: explain why the standard library is insufficient, prefer a reputable maintained package, and keep it in the narrowest module that needs it.
- Run `just tidy-check` after dependency changes. Do not hand-edit `go.sum`, and do not vendor dependencies unless the maintainer asks.

## Commits

- Use conventional commits: `type(scope): lowercase imperative summary`.
- Common scopes are `agent`, `llm`, `tool`, `session`, `tools`, `providers`, `stores`, `codjent`, `frontend`, `ci`, and `docs`.
- Keep each commit focused. Separate refactors, formatting sweeps, and dependency upgrades from behavior changes unless the change cannot work without them.

## Pull requests

- Keep each pull request to one feature or fix. Do not bundle unrelated provider or UI work.
- Start the description with `## What`: a plain-language paragraph for someone who has never seen the code. Then `## Why`: the problem and who it affected. Then `## Changes`: behavior and decisions a reviewer needs, not a file tour. Add `## Risk` only when there is one (security, migrations, limits, irreversible effects). End with `## Tested`: what was run and what was not, in words rather than pasted logs. Keep it under about 350 words unless the change is security- or migration-heavy.
- If a change adds user-facing copy, quote it in the description and explain why it is necessary and why removing it or revealing it only when relevant would not work.
- Never paste secrets, full logs, analytics, customer data, or machine-identifying details into commits, pull requests, issues, or reviews.
- Before opening a PR, review the diff for unrelated changes and generated files. After opening it, stay with it until CI and review feedback are terminal. Address failing checks and actionable feedback, push the fixes, and do not merge with red checks or unresolved findings. If a review finding is out of scope or wrong, remove the speculative code or explain the evidence instead of building around it.
- When reviewing a PR, judge it against `AGENTS.md` and this file. Recommend removing code that cannot work or does not belong rather than adding more code to support it. Flag changes the description does not mention, such as new dependencies, environment variables, test runners, or shared tooling edits.

## Releases

Go modules are released by pushing semver tags; there is no build artifact to upload for the library modules. GitHub Releases are human-readable notes and a convenient landing page.

### Versioning

- The root module uses `vX.Y.Z`; submodules use `<dir>/vX.Y.Z`, for example `providers/google/vX.Y.Z`.
- Optional modules require a released root version in their `go.mod`. When the core changes, bump and tidy that requirement before tagging the dependent module.
- Until v1, minor versions may contain breaking changes; call them out in the changelog.

### Cutting a core release

1. Start from a clean `main` with green CI: `git switch main && git pull`.
2. Pick the next version and move the `Unreleased` entries in `CHANGELOG.md` under `## [x.y.z] - YYYY-MM-DD`; add migration notes for breaking changes, then update the compare and version links at the bottom of the file.
3. Run `just check`.
4. Commit the release: `git commit -am "chore(release): vX.Y.Z"`.
5. Tag and push: `git tag vX.Y.Z` then `git push origin main vX.Y.Z`. The tag push opens a draft GitHub Release populated from the matching `CHANGELOG.md` section.
6. Review and publish the draft release. The tag is already live for Go modules even while the release is a draft.
7. Verify the module resolves: `go list -m github.com/ijaidev/gojent@vX.Y.Z`. The module proxy may take a minute to see a new tag.

### Cutting a submodule release

1. Confirm the core version it requires is already released.
2. Write the module's release notes from the merged pull requests; the root `CHANGELOG.md` covers only the core. Run the module checks, for example `just --justfile providers/google/justfile test`.
3. Tag with the directory prefix and push it: `git tag providers/google/vX.Y.Z` then `git push origin providers/google/vX.Y.Z`.
4. Create the GitHub Release manually and verify: `go list -m github.com/ijaidev/gojent/providers/google@vX.Y.Z`. The automated release workflow watches root `v*` tags only, because the root changelog covers only the core.

Do not tag or publish a release unless asked. Keep release notes user-facing: what changed, why it matters, and any migration step. Root releases create a draft automatically from `CHANGELOG.md`; submodule releases stay manual until a tool that understands directory-prefixed tags is worth the setup.
