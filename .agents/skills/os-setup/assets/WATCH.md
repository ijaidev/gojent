# Watch

## Roles

Mode definitions: `CONTRACT.md` § Modes. One watcher per role per PR. The caller sets the role: `as author` or `as reviewer`; never guess.

- The author is the only writer; it fixes or declines findings, pushes, and merges only when the grant names it. It watches until merged or closed.
- The reviewer reviews one head, posts a verdict, and stops. The named reviewer-merger waits for pending checks.

## Labels

Issue states: `os:triage` → `os:needs-info` | `os:ready` → `os:in-progress` → `os:done`. `os:blocked` and `os:wontfix` are terminal until a human changes them.

PR review states: `os:needs-review` → `os:in-review` → `os:changes` → `os:needs-review` → `os:in-review` → `os:approved`. `os:blocked` when a human must decide.
Type: `os:spec`, `os:ticket`, `os:bug`, `os:feature`, `os:refactor`, `os:chore`.

The issue stays `os:in-progress` from claim to merge. Review state lives on the PR.

## Transitions

- Author claim: issue `os:ready` → `os:in-progress`, before the spec read or any work.
- Author push: PR `os:needs-review`; issue stays `os:in-progress`.
- Reviewer claim: PR `os:needs-review` → `os:in-review`.
- Verdict: `os:in-review` → `os:changes` | `os:approved` | `os:blocked`.
- Fix push: PR `os:changes` → `os:needs-review`; issue stays `os:in-progress`.
- Head moved mid-review: `os:in-review` → `os:needs-review`.
- Merge: issue `os:done`; close.

## Label writes

Use REST. `gh pr edit` and `gh issue edit` fetch Projects-classic data and fail.

```bash
gh api --method POST repos/{owner}/{repo}/issues/<n>/labels -f 'labels[]=os:ready'
gh api --method DELETE repos/{owner}/{repo}/issues/<n>/labels/os:ready --silent
```

PRs use the same issues endpoint.

## Claims

- Author: set `os:in-progress` first, before reading the spec or creating the branch. Then push one branch per issue, `os/<issue>-*`, before editing. If the push is rejected, stop per `CONTRACT.md` § Rules. Keep the label until merge. A second slug is not a second claim.
- Reviewer: take a head with `os:needs-review` (or an explicit user request) and no live `os:in-review`. Post the claim marker, re-read; an earlier claim for the same head wins. Then replace `os:needs-review` with `os:in-review`, review that head, post `OS-REVIEW`. Clear the claim only if the head is still latest; if it moved, restore `os:needs-review` and post the verdict for the SHA you read.
- Stale: `os:in-review` with no claim activity for 30 minutes and no verdict on that head may be taken over. `os:in-progress` with no branch or PR and no issue activity for 30 minutes returns to `os:ready`. Delete a claim branch only when it has no PR and no commit newer than the claim.
- Labels are indexes; the branch ref and the `head=<sha>` markers are truth.

## Markers

```
<!-- os:review head=<sha> verdict=approve|changes|blocked -->
<!-- os:review-claim head=<sha> reviewer=<id> at=<ISO-8601> -->
<!-- os:fix head=<sha> review=<id> -->
<!-- os:watch role=author|reviewer last=<event-id|sha|iso> -->
<!-- os:blocked reason=<short> -->
```

## Dedupe

- review: `pr:<n>:review:<head>`
- fix: `pr:<n>:fix:<head>:<review-id>`
- watch: `pr:<n>:watch:<role>`
- merge: `pr:<n>:merge:<head>`

## Loop

1. Read your cursor. Fetch events newer than it.
2. Ignore your own actions and processed keys.
3. Act once per event by role: the author fixes or declines and pushes; the reviewer reviews the new head, posts a verdict, and stops. Never re-act on the same key in a session. Only the event handler changes labels.
4. Update the cursor comment.
5. Quiet PR → do nothing, keep watching. Not a stop.

## Event order

1. **Conflicts** — the author reports and rebases only if safe; the reviewer reports only; never force-push a shared branch.
2. **Review threads** — the author fixes or declines each finding with evidence per `REVIEW.md` § Loop; on a fix push: clear `os:changes`, set `os:needs-review`, post `OS-FIX`. The reviewer claims the head and posts a verdict. Never silently complies.
3. **CI** — the author fixes only the diff's failures; flake earns one rerun; unrelated → report. The reviewer reports.

## Merge gate

The `ship` grant is per task and names the merger: `author` or `reviewer`. A config default of `ship` is not a grant. The named merger checks the gate every cycle, not only on an event.

- Required checks green on the latest head.
- `APPROVE` on that head by a reviewer who is not the author.
- No open blocker/should.
- No human change request.
- Mergeable; branch current.
- No gate weakening.

Green with a grant: use the repo's merge method; never `--admin`; never merge `BEHIND` or `DIRTY`.
Green without a grant: keep `os:approved`, comment `Human merge required.`, keep watching.
Not green: report what is missing.

## Post-merge

The merger, in order:

1. Replace the ticket's state label with `os:done`; close it as completed.
2. Promote the frontier: open `os:ticket` children of the spec with a brief, all blockers closed, and no state label get `os:ready`.
3. Close the spec only when every `R#` (or bugfix requirement) maps to a merged PR's `Requirements` section and every child ticket is closed. Set `Status: shipped`, bump `Updated:`, close it. If every child is closed but an `R#` is uncovered, leave it open and comment the gap. Do not comment while any child is open.
4. Delete the branch if the repo does.

Never close a spec with an open child or an uncovered `R#`.

## Stop

Merged, closed, user says stop, the `REVIEW.md` § Loop limits, a security-sensitive or irreversible change, a gate weakening, or `os:blocked`. The reviewer stops after a verdict unless it is the named merger waiting on checks; the author watches through `os:approved`.

## Limits

- Native watcher if available; else one bounded poll per turn. At most one event per cycle; honor a ≥60s cooldown when the harness supports it. No `sleep` loops.
- GitHub GraphQL is separate from REST; fall back on exhaustion.
- Never claim to watch after the process ends. Leave the cursor.
