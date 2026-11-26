---
name: os-review
description: "Manual. Review a PR against its spec with evidence and a machine-readable verdict. Run when the user asks to review a PR, for example /os-review."
---

# Review

Reviewer mode (`CONTRACT.md` § Modes).

## Preflight

- Read `.openship/CONTRACT.md`; absent → stop: `/os-setup` first.
- Read `.openship/REVIEW.md`, the PR, and its `Issue` / `Spec` / `Brief` links.
- Never review your own PR. If you authored it, stop.
- Read `WATCH.md` § Claims and claim the head. An earlier live claim on that head → stop.
- Never change the issue state.
- Read the artifacts in `CONTRACT.md` § Preflight order.
- Missing links → search linked issues and commits. Still missing → quality-only review; state `Spec: none`.
- Diff > 400 changed lines or > 10 files, not mechanical → ask to split before line-by-line review. Security or data migration may proceed.

## Method

Follow `REVIEW.md` § Scope, § Two axes, § Severity, § Quality checks, § Evidence, § Gate integrity.

1. Pin the head SHA. Read the diff against the base.
2. Map every requirement to pass, fail, or not verifiable. Cite evidence.
3. Re-run at least one claimed check; the declared set is per `CONTRACT.md` § Rules; drive the real surface once for behavior.
4. Record findings with a concrete failure, spec violation, or exploit path.

## Verdict

Choose `APPROVE`, `CHANGES`, or `BLOCKED` per `REVIEW.md` § Verdict. Read `ARTIFACTS.md` § Review; post one `OS-REVIEW` comment from that template, including the review marker from `WATCH.md` § Markers.

Apply the verdict transition in `WATCH.md` § Transitions. If the repo requires a formal GitHub approval, submit `gh pr review --approve` only for `APPROVE`.

## After

Follow `REVIEW.md` § Loop and § Rules. If the brief names you merger and the verdict is `APPROVE`, run the merge gate in `WATCH.md` § Merge gate. Post and stop per `REVIEW.md` § Loop. To keep watching, load `/os-pr-watch as reviewer` only on user request or as the named merger waiting on checks. On merge, run the post-merge order in `WATCH.md`.
