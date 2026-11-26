# Review

## Scope

Review the head SHA, not the author's report. Read spec → ticket → brief → comments newer than `brief.at` → diff → checks, before the PR narrative. Re-run one claim.

## Two axes

1. **Spec compliance** — every `R#` and `S#` (or a bugfix spec's current, expected, and unchanged behavior): pass, fail, or not verifiable.
2. **Quality** — correctness, security, tests, scope, operability.

## Severity

- **blocker** — wrong behavior, security, data loss, broken gate, spec violation. Must fix.
- **should** — real bug or regression in the intended case. Fix before merge.
- **nit** — style, naming, preference. Never blocks; omit unless it hides a defect.

## Quality checks

- Tests fail before the fix and pass after; they assert behavior, not implementation. A snapshot or assertion-free test is not proof.
- Behavior: drive the real surface once. Tests alone are `best-effort`.
- Fresh context: prefer a session that did not write the code; a separate thread is enough.
- Security: trust boundaries, authorization, injection, secrets, and data exposure.
- Operability: failure modes, retries, timeouts, logs, migrations, rollback.

## Evidence

- A finding survives only when a run proves it. A spec violation is proved by quoting the spec.
- A finding needs a concrete failure, a spec violation, or a plausible exploit path.
- Do not trust the PR body; re-run at least one claimed check. For behavior, drive the surface; a hands-on observation is evidence. If you can neither run nor exercise it, verdict `BLOCKED`, not `APPROVE`.
- Run focused tests when reading is insufficient. Do not re-run the full suite by reflex.
- "Should work" is not evidence. "No findings" is valid; do not invent.

## Gate integrity

Any of these is a blocker until a human signs off:

- Deleted or skipped tests.
- Weakened assertion, timeout, or threshold.
- Regenerated snapshot without reading it.
- Placeholder, TODO, or stub shipped as behavior.
- Tautological test (asserts the mock, not the behavior).
- Suppression pragma, swallowed error, or `--no-verify`.
- CI step removed, disabled, or made optional.
- Spec edited to match the code.
- Unit test presented as end-to-end proof.
- Environment sniffing that bypasses a check.

## Verdict

- **APPROVE** — no blocker/should; evidence covers the diff; head SHA recorded.
- **CHANGES** — at least one blocker/should with a required fix.
- **BLOCKED** — cannot verify the claim, missing artifact, or conflicting spec.

## Loop

- Max 3 rounds. Stop when a round returns only nits.
- Review one head, post the verdict, stop. A new head is a new review.
- Re-review the delta and open IDs, not the whole PR.
- The author checks each finding against the spec: `fixed` or `declined` + evidence.
- A declined blocker is escalated, not ignored.
- After the round limit without convergence: `os:blocked`, stop.

## Rules

- Never edit, push, or resolve threads. Merge only when the brief names you merger, and only the head you approved.
- Post every verdict as an `OS-REVIEW` comment; approving silently is a defect.
- Findings are claims; the author may decline with evidence.
- No scope expansion; follow-ups go to the issue.
- Cross-provider review is optional; say when unavailable.
- Review the code, not the author.
