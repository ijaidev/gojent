---
name: os-setup
description: "Manual. Install or refresh OpenShip in this repo: AGENTS.md pointer, .openship references, config, labels. Run when the user asks to set up OpenShip, for example /os-setup."
---

# Setup

One install per repo. Idempotent. No runner, scheduler, queue, or issues.

## Preflight

- Confirm the repo root: `git rev-parse --show-toplevel`.
- Resolve `assets/` relative to this `SKILL.md`, not the current directory.
- Read an existing `.openship/config.json` and `AGENTS.md`; never assume a fresh repo.
- Invalid `config.json`, a non-integer `config.version`, or one newer than `assets/config.default.json` → stop and show it; never overwrite. Older versions are migrated during Apply.
- Malformed `<!-- os:begin -->` / `<!-- os:end -->` markers → stop and show them.

## Plan

Build the full write plan before changing anything.

1. **References.** `CONTRACT.md`, `ARTIFACTS.md`, `REVIEW.md`, `WATCH.md`, `DEBUG.md`, `labels.tsv`.
   - Missing → copy from `assets/`.
   - Present and identical → skip.
   - Present and different → approved → copy; declined → keep local and mark the install `partial`.
2. **Version.** `VERSION` is metadata, not a reference.
   - Every reference current → write `assets/VERSION` to `.openship/VERSION`.
   - Any reference declined → leave `.openship/VERSION` unchanged and report `partial — version not bumped`.
3. **Pointer.** Render `assets/AGENTS.block.md`.
   - Markers `<!-- os:begin -->` and `<!-- os:end -->` exist → replace the block.
   - No markers → append one blank line, then the block.
   - Preserve every byte outside the markers.
   - Replace `<!-- os:version … -->` (empty or filled) with the version in `.openship/VERSION`; if no version exists, keep the existing marker.
4. **Config.** Merge `assets/config.default.json` into `.openship/config.json`: existing keys win, missing keys are added. Never delete a key, except retired keys (`commands`). Verification commands come from the repo (`CONTRACT.md` § Rules), so setup does not store them.
   - `version` always becomes the default's. Migrate from the config's version upward (`1 → 2` removes `commands`); no known step → stop.
5. **Labels.** Registry is `labels.tsv` (tab-separated: name, color, description), installed with the references. When authenticated, compare with `gh label list`; create or update the set, never delete.

## Confirm

- No writes, every reference identical to `assets/`, and every label exists with the expected color and description → print `OpenShip is up to date (<version>).` and stop.
- Only missing files with no conflicts → apply; os-setup was explicitly invoked.
- A changed reference, a changed `AGENTS.md` pointer, or a config value that would change behavior → show one combined diff and ask once.
- Schema migration to the default version and deleting retired keys change no behavior: apply and report them; do not ask.
- Never ask per file.

## Apply

```bash
SKILL_DIR="<absolute dir of this SKILL.md>"
REFS="CONTRACT.md ARTIFACTS.md REVIEW.md WATCH.md DEBUG.md labels.tsv"
mkdir -p .openship
# Create missing references; copy approved diffs before this check.
for f in $REFS; do
  [ -e ".openship/$f" ] || cp "$SKILL_DIR/assets/$f" ".openship/$f"
done
# A remaining difference is a declined or local edit: keep it and stay partial.
partial=0
for f in $REFS; do
  cmp -s "$SKILL_DIR/assets/$f" ".openship/$f" || partial=1
done
[ "$partial" -eq 0 ] && cp "$SKILL_DIR/assets/VERSION" .openship/VERSION
```

- Copy an approved reference explicitly before the check; the loop only creates missing files.
- Write the merged `AGENTS.md` and `.openship/config.json`. Migrate an older config to the default `version` before writing.
- Create labels:

```bash
while IFS=$'\t' read -r name color desc; do
  [ -z "$name" ] && continue
  gh label create "$name" --color "$color" --description "$desc" --force
done < .openship/labels.tsv
```

- Apply labels from the installed `.openship/labels.tsv`, not `assets/`; a kept local edit stays authoritative.
- If `gh` is missing or unauthenticated: write all files, skip labels, print the loop above.
- Never delete labels that are not in the file. A repo may have its own labels.
- Never commit, push, open issues, install CI, or create a GitHub Action.

## Report

Print, in this order:

1. Files written / skipped.
2. Config: migrated to `<default version>` with retired keys removed, or unchanged.
3. Labels applied, or skipped when `gh` is unavailable.
4. Version, or `partial — version not bumped`.
5. One next command:
   - fuzzy idea → `/os-grill`
   - clear change → `/os-spec`
   - existing issue → `/os-triage`

Recommend committing `AGENTS.md`, `.openship/`, and `skills-lock.json`.

## Refresh

After `npx skills update`, run `/os-setup` again. A full update advances `VERSION`; a declined reference leaves the install partial and the stamp unchanged. A locally edited reference is never clobbered without a diff.
