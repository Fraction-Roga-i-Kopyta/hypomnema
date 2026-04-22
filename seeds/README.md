---
type: documentation
purpose: seed-examples
---

# Memory Seeds

This directory holds **format examples**, not real mistakes. They exist so a new Hypomnema user can see what a memory entry looks like and which fields are worth populating.

## What to do with them

**Option A — read and forget.** Skim two or three files to get the shape. Then write your own mistakes by analogy — Claude will propose the format on its own after the first "record this in memory".

**Option B — copy into `mistakes/`.** If you've actually hit one of these bugs, move the file into `~/.claude/memory/mistakes/`, drop the `seed: true` field, and update `created` and `triggers` to match your situation.

**Option C — delete the directory.** If the seeds are in the way (firing triggers, polluting scoring), run `rm -rf ~/.claude/memory/seeds/`. Nothing else depends on them.

## Why `seed: true` matters

The `seed: true` frontmatter field tells the scoring system **not to count these files as real experience**:
- Excluded from the bootstrap counter (5 user mistakes are needed to leave bootstrap mode — seeds don't count).
- Deprioritised in SessionStart injection.
- UserPromptSubmit triggers still fire on them, but with reduced weight.

This lets seeds serve as a reference without polluting actual memory.

## What's here

| File | Domain | Who it applies to |
|---|---|---|
| `wrong-root-cause-diagnosis.md` | general | everyone — Claude's top universal debugging mistake |
| `shell-escape-special-chars.md` | shell | everyone |
| `datetime-naive-timezone.md` | data, code | analysts, developers |
| `file-path-spaces-unquoted.md` | shell | everyone |
| `encoding-utf8-vs-cp1251.md` | data | data work, text processing |
| `git-committed-secrets-history.md` | git | developers |
| `source-url-rot-no-local-copy.md` | research | researchers, writers |
| `api-endpoint-silently-deprecated.md` | api | developers |
