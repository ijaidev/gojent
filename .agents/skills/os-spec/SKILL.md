---
name: os-spec
description: "Manual. Turn a clear intent into a durable behavior spec as a GitHub issue. Run when the user asks to write a spec or turn an idea into an issue, for example /os-spec."
---

# Spec

Produce one spec. No code, no tickets.

## Preflight

- Read `.openship/CONTRACT.md`; absent → stop: `/os-setup` first.
- Read `.openship/ARTIFACTS.md` and `.openship/config.json`.
- Need a clear problem and outcome. Fuzzy → load `/os-grill` per `CONTRACT.md` § Preflight. If the hop budget is exhausted, stop and report: `/os-grill` required. Announce: `No clear outcome — grilling first.`
- Right-size per `CONTRACT.md` § Rules. A typo, dependency bump, docs fix, or mechanical change does not get a spec; say so and route to `/os-triage`.
- Read the code, tests, and docs the behavior touches. Ground every claim.

## Split

Two or more independent outcomes → ask once whether to split; recommend split. Do not write until answered. On split, stop and run `/os-spec` once per outcome; if declined, write one spec and note the combined scope.

## Write

- Use the feature or bugfix template from `ARTIFACTS.md`. Follow its rules.
- Set `Status: ready` and `Updated:` to now at creation.
- Open questions that can change behavior → load `/os-grill`; never create a spec with unresolved behavior.

## Create

Show the full spec once, then create it:

```bash
gh issue create --title "<title>" --body-file <tmp> \
  --label os:spec --label os:<bug|feature|refactor>
```

Print the URL and: `Next: /os-tickets #N.`

## Spec home

- `issue`: create the issue; no file.
- `repo`: write `specs/<slug>.md`, then open an issue whose body is a pointer.
- No GitHub auth: use `repo` and say the GitHub steps were skipped.

## Rules

- Default: one spec per outcome.
- Do not create tickets, branches, or PRs here.
