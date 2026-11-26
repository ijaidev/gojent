---
name: os-tickets
description: "Manual. Slice a spec into tracer tickets with briefs and native dependencies. Run when the user asks to break a spec into tickets, for example /os-tickets."
---

# Tickets

Turn one spec into vertical tickets. Each is one author, one context window, one demo.

## Preflight

- Read `.openship/CONTRACT.md`; absent → stop: `/os-setup` first.
- Read the spec per `CONTRACT.md` § Artifacts, its comments, `.openship/ARTIFACTS.md`, and `config.json`.
- Missing spec → chain to `/os-spec` per `CONTRACT.md` § Preflight. Announce each hop; stop at the budget.
- Spec has open behavior questions → stop; resolve them in the spec first.
- Spec touches 3+ independent outcomes → recommend splitting the spec; ask before slicing.

## Slice

- **Vertical**: each ticket delivers observable behavior, not a layer.
- **Tracer first**: the thinnest end-to-end path. Then widen.
- **Prefactor** before the feature that needs it.
- **Wide refactor**: expand → migrate → contract, one ticket per step.
- Dependencies form a DAG. No cycles. Every `Blocked by` ticket is created in this batch or already open.
- Only the frontier (no open blockers) gets `os:ready`. Never use `os:blocked` for dependency waiting; it is for human decisions.

## Size

Default is 3–7 tickets. If the slice lands outside it, stop and ask one question with the count, the options, and your recommendation; do not create until answered.

- **<3**: keep 1–2, or fold into one ticket. Recommend keep when each ticket has a distinct demo; recommend fold when one ticket covers all acceptance and still fits one context window.
- **>7**: allow more, or split the spec. Recommend split when the spec has 3+ independent outcomes or multiple DAGs; recommend allow when the tickets share one DAG and each still fits one context window.

Recommend from: spec scope, independent outcomes, one demo per ticket, one context window, dependency depth, and risk. On split, stop; do not create tickets. Print: `Split the spec first: /os-spec.`

## Brief

Post one brief per ticket from the `ARTIFACTS.md` Brief template. Any open question means the ticket is not `os:ready`.

## Create

Show a table: title, type, blocked by, demo, and the first ticket's brief. Then create each:

```bash
gh issue create --title "<title>" --body-file <tmp> \
  --label os:ticket --label os:<bug|feature|refactor|chore>
```

Then add dependencies:

```bash
id=$(gh api repos/{owner}/{repo}/issues/<blocker> --jq .id)
gh api --method POST repos/{owner}/{repo}/issues/<blocked>/dependencies/blocked_by \
  -F issue_id="$id"
```

If the dependency API is unavailable, write `Blocked by: #N` in the body and add a parent task list. Never invent a native link.

Post each brief. Set `os:ready` on the frontier only.

## Report

Print the DAG and the frontier. `Start: /os-implement #<frontier>.`

## Rules

- Do not open PRs.
- A ticket narrows the spec: scope, acceptance, verification. Behavior lives in the spec (`CONTRACT.md` § Authority).
