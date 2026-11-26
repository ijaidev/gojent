---
name: os-pr-watch
description: "Automatic. Watch a PR and act on new events until merged, closed, or stopped. Load after opening a PR or when the user asks to watch one."
---

# PR Watch

Watch one PR. The role comes from the caller, not from this skill.

## Load

1. Read `.openship/CONTRACT.md`; absent → stop: `/os-setup` first.
2. Read `.openship/WATCH.md` and the PR's `Issue` / `Spec` / `Brief`.
3. Role: from the invocation (`as <role>`). Missing → infer from context; ambiguous → ask once.
4. Confirm the role's lock per `WATCH.md` § Claims.
5. Announce: `Watching PR #N as <role> until merged, closed, or stopped.`
6. Find the cursor by the marker in `WATCH.md` § Markers. Create it if absent.
7. If the user says another watcher has your role, stop and report. A cursor alone is not proof of an active watcher.

## Cycle

Follow `WATCH.md` § Loop and § Limits.

## Merge

Follow `WATCH.md` § Merge gate.

## Stop

Per `WATCH.md` § Stop. On merge, run the post-merge order there. On stop without merge, say what remains and who must act.

## Limits

Follow `WATCH.md` § Limits. If the harness has no native watch, poll once per turn and say: `Watching only while this session is active; re-invoke /os-pr-watch #N as <role> to continue.`
