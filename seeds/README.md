# Memory Seeds

This directory holds **format examples**, not real mistakes. They exist so a new Hypomnema user can see what a valid v2 memory file looks like and which fields are worth populating. Each seed is a real, parseable v2 `mistake` file: required `name` / `description` / `type` plus the mistake-specific fields (`severity`, `recurrence`, `scope`, `root-cause`, `prevention`).

## What to do with them

**Option A — read and forget.** Skim two or three files to get the shape, then write your own mistakes by analogy. Claude will propose the format on its own after the first "record this in memory".

**Option B — copy into a store.** If you've actually hit one of these bugs, copy the file into a flat native store:

- Global (applies in every project): `~/.claude/memory-global/`
- A single project: `~/.claude/projects/<slug>/memory/`

Stores are **flat** — there are no `mistakes/` / `strategies/` subdirectories; the `type:` frontmatter field decides the logical type. After copying, update `created`, `keywords`, and `description` to match your situation. Set `status: pinned` on any file you never want to decay.

**Option C — ignore or delete.** Nothing depends on these files. Delete the ones you don't want, or leave the directory alone.

## Notes

- **No `seed:` / `decay_rate:` fields.** Those were v1 constructs and are dead in v2 (0 code hits). There is no bootstrap counter and no trigger mechanism — ranking is done by `memoryctl` from `keywords` / `domains` / `description` overlap plus recency and effectiveness. Do **not** hand-set `ref_count` or `effectiveness`; the sidecar manages them.
- **Secrets gate.** `git-committed-secrets-history.md` ships hazard-looking examples (`AKIA…`, `ghp_…`, `sk_live_…`) as documentation, not real secrets. The `memoryctl guard` hook would flag them, so the shipped `.secretsignore.default` whitelists `seeds/**`. If you copy that seed into a store outside a `seeds/` path and the guard blocks it, add its path to `~/.claude/memory-global/.secretsignore` (the examples are documentation of hazards, not hazards).

## What's here

| File | Domains | Who it applies to |
|---|---|---|
| `wrong-root-cause-diagnosis.md` | general | everyone — Claude's top universal debugging mistake |
| `shell-escape-special-chars.md` | shell | everyone |
| `datetime-naive-timezone.md` | code, data | analysts, developers |
| `file-path-spaces-unquoted.md` | shell | everyone |
| `encoding-utf8-vs-cp1251.md` | data | data work, text processing |
| `git-committed-secrets-history.md` | git, security | developers |
| `source-url-rot-no-local-copy.md` | research, writing | researchers, writers |
| `api-endpoint-silently-deprecated.md` | api | developers |
