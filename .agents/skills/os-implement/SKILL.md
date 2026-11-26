---
name: os-implement
description: "Manual. Implement a ready ticket on its own branch, verify, and open a PR. Run when the user asks to implement a ticket or open a PR, for example /os-implement."
---

# Implement

Author mode (`CONTRACT.md` § Modes). One ticket, one branch, one PR.

## Preflight

1. Read `.openship/CONTRACT.md`; absent → stop: `/os-setup` first.
2. Read `.openship/config.json`.
3. Resolve the ticket: `gh issue view N`.
   - Spec, not ticket → chain to `/os-tickets` per `CONTRACT.md` § Preflight. If the work is still fuzzy, stop and ask for `/os-grill`.
   - Existing `os/<issue>-*` branch or PR → resume only if you are the writer; otherwise stop. Never open a second.
   - `os:in-progress` with no branch or PR → check `WATCH.md` § Claims: live claim → stop and report; stale → restore `os:ready`.
   - Not `os:ready` → stop. Check dependencies; open blocker → stop and name it.
4. **Claim first.** Unless the user asked to analyze only or `config.autonomy` is `advise`, replace `os:ready` with `os:in-progress` before reading the spec, touching code, or creating the branch. Read `WATCH.md` § Claims and use its REST pattern. Keep it until merge. If the brief sets `advise` or you stop before Work, restore `os:ready`.
5. Read the artifacts in `CONTRACT.md` § Preflight order. Quote the `R#`/`S#` or acceptance criteria you will satisfy. Ticket without a brief → small and self-contained: read `ARTIFACTS.md` § Brief, draft it, and confirm once. Otherwise: spec exists → chain to `/os-tickets`; no spec → stop and ask.
6. Spec vs ticket/comment conflict → stop and ask. Never pick silently.

## Autonomy

Apply `CONTRACT.md` § Autonomy; precedence is there. Stop at the level's boundary: `advise` after Preflight, `build` after commits. `deliver` opens and fixes the PR; `ship` also merges per that section.

## Work

1. `git fetch`; resume any `os/<issue>-*` branch, or create and push `os/<issue>-<slug>` from the default branch.
2. Reproduce the bug or write the failing test first. For a feature, test the smallest behavior.
3. Implement the smallest diff that satisfies every requirement. No drive-by edits; every changed line traces to the ticket and the spec.
4. Run every check the repo declares, in `CONTRACT.md` § Rules order, plus the brief's `Verification`; verify behavior by hand last. Classify: `verified`, `best-effort`, or `reverted`.
5. Commit in logical units: `<type>(<scope>): <what> (#N)`.
6. Read `ARTIFACTS.md` § PR; push and open a ready PR from that template, including the `Issue`, `Spec`, and `Brief` headers.
7. Apply the author-push transition in `WATCH.md` § Transitions.
8. Load `/os-pr-watch as author` unless `config.watch = "manual"` or the user said not to watch.

## Stops

- A failing check: follow `.openship/DEBUG.md`; after 3 failed attempts → `<!-- os:blocked reason=... -->`, label `os:blocked`, report.
- Stale claim → apply `WATCH.md` § Claims; restore `os:ready` and restart; report.
- Stale claim branch → apply `WATCH.md` § Claims; delete it and restart; report.
- Missing credential, ambiguous behavior, or a gate you must weaken → stop and ask.
- Do not fix unrelated findings; file them.
