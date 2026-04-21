# Release checklist

Manual gate before any `git tag vX.Y.Z`. CI covers most of this on every
push, but a tagged release is a contract with users — re-verify by hand.

## 1. Repo hygiene

- [ ] `git status` — working tree clean.
- [ ] `git pull` — no unmerged upstream changes on `main`.
- [ ] `CHANGELOG.md` — new `## [X.Y.Z] — YYYY-MM-DD` section with:
  - Summary (1–2 lines)
  - `### Added` / `### Changed` / `### Fixed` / `### Removed`
  - Migration notes if frontmatter schema or WAL grammar changed.
- [ ] `README.md` — version badge / install snippet reference the new tag.

## 2. Automated checks (also run by CI — re-run anyway)

- [ ] `bash hooks/test-memory-hooks.sh` — `Passed: N / N`, zero failures.
- [ ] `find . -name "*.sh" -not -path "./.git/*" -not -path "./docs/*" -print0 \
      | xargs -0 shellcheck -e SC1091 -S error` — clean exit.
- [ ] `bash install.sh --dry-run` — exits 0, no unexpected paths.

## 3. Clean-VM bootstrap (the real release gate)

CI does this automatically; still confirm by running one of these
locally before cutting the tag.

### Docker (preferred — closest to a fresh user)

```bash
docker run --rm -it \
  -v "$(pwd)":/repo:ro \
  ubuntu:24.04 bash -c '
    apt-get update && apt-get install -y --no-install-recommends \
      bash jq sqlite3 perl ca-certificates curl git
    curl -LsSf https://astral.sh/uv/install.sh | sh
    export PATH="$HOME/.local/bin:$PATH"
    useradd -m testuser
    su - testuser -c "
      cp -r /repo ~/hypomnema
      cd ~/hypomnema
      mkdir -p ~/.claude
      bash install.sh
      bash hooks/test-memory-hooks.sh
      bash uninstall.sh
      # Confirm nothing of ours is left behind:
      find ~/.claude -type l -name 'memory-*' -o -name 'wal-compact.sh'
    "
'
```

Expected: test suite green, final `find` produces no output.

### Alternative: fresh macOS user account

If you have one lying around (test VM, second Mac): same flow,
`./install.sh && bash hooks/test-memory-hooks.sh && ./uninstall.sh`.

## 4. Version bump

- [ ] Decide version per SemVer:
  - `PATCH` — bugfix, no schema / WAL / hook-contract change.
  - `MINOR` — new behaviour, backwards-compatible frontmatter fields.
  - `MAJOR` — breaking schema change or hook-contract break.
- [ ] Update version references if they exist (README, `CHANGELOG.md`).

## 5. Tag + push

```bash
git tag -a vX.Y.Z -m "Release X.Y.Z"
git push origin vX.Y.Z
```

Do NOT push before step 3 is green on a clean VM — pulled tags are a
pain to retract once other clones have fetched them.

## 6. Post-release smoke

- [ ] Trigger CI on `main` at the tag (GitHub Actions) — both
      `ubuntu-latest` and `macos-latest` jobs green.
- [ ] Re-clone into `/tmp` from GitHub, `install → test → uninstall`.
      (Catches cases where `main` worked but the tag snapshot has a
      stale submodule or missing file.)

## Known acceptable warnings

- `shellcheck -S warning` reports ~120 SC2034 (unused variables) —
  most are deliberate in tests (exit codes captured for side-effect).
  CI enforces `-S error` only. Treat warning-level cleanup as
  background work, not a release blocker.
- `install.sh` prints "Backup saved: settings.json.backup-hypomnema"
  even in `--dry-run` — cosmetic, tracked separately.
