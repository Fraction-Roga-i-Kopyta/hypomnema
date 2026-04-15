# Migration Guide

## v0.7 → v0.8

### Required actions: none

v0.8 is backward compatible. Existing memory files continue to work without edits.

### What changed

1. **Pre-flight checks in install.sh.** Missing `jq`/`perl`/`awk`/`bash 3.2+` now fails fast with a clear message instead of silently corrupting `settings.json`.

2. **Cyrillic / non-ASCII bodies fully tokenized.** `evidence_from_body` rewritten in perl with UTF-8 awareness (`\w{4,}` under `/u`). If you previously added explicit `evidence:` frontmatter to non-ASCII files as a workaround, you can keep it (still respected) or remove it — auto-extraction now works.

3. **Perl regex injection fix.** Section markers in injected context are now safe against memory filenames containing regex metacharacters (e.g. `foo)bar.md`). `_perl_re_escape` helper added.

4. **WAL read locking.** Feedback-loop extraction in the Stop hook now acquires the WAL lock before reading. No user action needed; eliminates undercount of `inject` events when multiple sessions run concurrently.

5. **`set -o pipefail` everywhere.** All executable hooks/bin scripts now have it. Test 23 enforces this at meta level.

6. **Portable stat helpers.** `lib/stat-helpers.sh` provides `_stat_mtime` and `_stat_size` — replaces five inline `stat -f %m vs stat -c %Y` blocks across `session-start.sh` and `session-stop.sh`.

7. **Repo-root `CLAUDE.md`.** New agents entering the repo learn the memory protocol on first read.

8. **Docs:** `docs/QUICKSTART.md`, `docs/TROUBLESHOOTING.md`, `docs/FAQ.md`, `docs/MIGRATION.md`.

9. **Interactive install wizard.** `./install.sh --discover` scans dev folders for git repos and proposes `projects.json` entries. `./install.sh --patch-claude-md` adds the four-line memory section to your global CLAUDE.md. `--dry-run` for preview.

10. **Externalized constants.** `~/.claude/memory/.config.sh` overrides defaults (`MAX_FILES`, decay thresholds, etc.). `templates/.config.sh.example` provided.

11. **Architecture refactor.** `hooks/session-start.sh` decomposed into `hooks/lib/{parse-memory,score-records,build-context}.sh` modules. If you forked any hook, rebase against the new structure — most edits move into the relevant lib module.

### Optional cleanup

- Delete legacy explicit `evidence:` blocks added as Cyrillic workaround (now redundant — but harmless if kept).
- If you customized any hook, re-apply your patch on top of the new lib/ structure (see `git log -p` for the v0.8 refactor commits).

### Rollback

```bash
cd /path/to/hypomnema
git checkout v0.7.1
./install.sh
```

Memory files are unaffected; only the hooks revert.

### Known non-issue: multi-word domains

`domains:` in frontmatter is space-separated after YAML parsing. Multi-word values like `"machine learning"` get split into `"machine` + `learning"`. Use kebab-case (`machine-learning`) instead — this is the existing convention across all seeds and templates.
