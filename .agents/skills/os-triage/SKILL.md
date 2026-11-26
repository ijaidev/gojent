---
name: os-triage
description: "Manual. Classify an incoming issue and route it: needs info, duplicate, wontfix, ticket, or spec. Run when the user asks to triage or route an issue, for example /os-triage."
---

# Triage

One issue in, one route out. Read-only except labels, comments, and a reasoned close for duplicate or wontfix.

## Preflight

- Read `.openship/CONTRACT.md`; absent → stop: `/os-setup` first.
- Read the issue, comments, repo, `.openship/ARTIFACTS.md`, and `.openship/config.json`.
- Already `os:ticket` → check the brief and `os:ready`; route to `/os-implement`.
- Already `os:spec` → route to `/os-tickets`.
- Search before asking: `gh issue list --search`, code search, history.
- Reproduce a bug when cheap. Run the stated command; evidence beats speculation.

## Classify

Ask at most 3; follow `CONTRACT.md` § Questions.

| Signal | Route |
|---|---|
| Duplicate or already fixed | Link it; close or `os:wontfix` |
| No repro / missing info | `os:needs-info` + exact command/output needed |
| Typo, docs, dependency, mechanical | Ticket: brief + `os:ticket`, `os:ready` |
| Clear bug with repro | Ticket: brief + regression test, `os:ready` |
| New behavior, data model, or spec-sized per `CONTRACT.md` § Rules | Spec |
| Security | No public details; use the repo security policy |

Apply `CONTRACT.md` § Rules before choosing Spec vs Ticket.

## Route

- **Small**: post the brief (`spec=none`; the ticket is the only artifact), add `os:ticket` + type + `os:ready`. Print `Start: /os-implement #N.`
- **Large**: print `Next: /os-spec — <one-line outcome>.` Chain only if the user asked to proceed.
- **Needs info**: one comment with numbered questions and the exact evidence required. Stop.
- **Duplicate / won't fix**: link the reason; close only with a reason.

## Rules

- Do not open PRs or create a spec-sized ticket.
- A bug without a repro is not ready; ask for one.
- Do not relabel a human decision without saying so.
- Use the Brief template in `ARTIFACTS.md`; never restate behavior.
