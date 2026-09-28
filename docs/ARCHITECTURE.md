# Architecture

Map of how hypomnema v2 turns Claude Code hook events into ranked memory
injection and back into effectiveness measurement. Read this before editing a
shim or a `memoryctl` verb; refer to it when reviewing a PR that touches
control flow.

The code side of truth is `hooks/v2/` (thin shims), `cmd/memoryctl/` (CLI
entrypoints), and `internal/` (the Go engine). This document explains *how they
fit together*; it does not replace the code, `CLAUDE.md` (the agent protocol),
or the per-release CHANGELOG.

> **v2 in one line.** The Go binary `memoryctl` is primary and mandatory. Each
> hook is a ~5-line shell shim that `exec`s a `memoryctl` verb. There is no
> bash logic, no `hooks/lib/`, no FTS5, and no second scoring pipeline — all
> retired with v1 (see git tag `v1.1.2`).

## The hooks

`install.sh` copies six shims into `~/.claude/hooks/v2/` and registers them in
`settings.json`. Each shim resolves `memoryctl` (fail-open: `exit 0` if the
binary is absent) and `exec`s one verb, forwarding stdin (the hook envelope)
and preserving the exit code.

| Event | Matcher | Shim | Verb | Purpose |
|---|---|---|---|---|
| `SessionStart` | — | `session-start.sh` | `inject --event=SessionStart` | Rank native facts, inject top-K into `additionalContext`; on `compact` re-ranks against the compaction summary, on `clear` against the empty prompt — either way without render-dedup |
| `UserPromptSubmit` | — | `user-prompt-submit.sh` | `inject --event=UserPromptSubmit` | Reactive re-rank with prompt tokens; inject newly-relevant facts |
| `PreToolUse` | `Write\|Edit` | `pre-tool-write.sh` | `guard` | Secrets gate on memory-path writes (`exit 2` blocks) |
| `PreToolUse` | `Skill` | `skill-active.sh` | `skill-active` | Record the activated skill for this session |
| `PostToolUse` | `Skill` | `skill-learnings-inject.sh` | `skill-inject` | Inject accumulated skill-learning facts for that skill |
| `Stop` | — | `session-stop.sh` | `close` | Attribute outcomes → WAL → effectiveness/decay → self-profile |

Compaction is handled on `SessionStart` with `source=compact` (and `/clear`
with `source=clear`), not by a separate hook: **no `PreCompact`/`PostCompact`
hook is registered**. `PreCompact` was retired with v1. Claude Code does have
a `PostCompact` event (fields `trigger`, `compact_summary`; no
`additionalContext`), but it carries no way to reach the model's context —
`SessionStart` with `source=compact` is the event whose `additionalContext`
actually gets seen, so that is where hypomnema re-renders memory instead. The
shims carry zero logic; everything below happens inside `memoryctl`.

## End-to-end data flow

```
SessionStart ──► memoryctl inject --event=SessionStart ─────────┐
   project.Detect(cwd) + domains.Infer(git)                     │
   keywords ← prompt-absent: cwd basename, branch, changed      │
             filenames, recent commit subjects                  │
   native.List(project store + global store)                    │
   sidecar reproject if content_sha / mtime drift               │
   rank.TopK(keywords, candidates)  ── pure, I/O-free           │
     filter: status ∈ {active, pinned}; scope ∈ {project,global}│
   emit top-8 (≤2.5KB/body, ≤8KB total) as `# Memory Context`   │
     in additionalContext JSON                                  │
   WAL: inject|<slug>|<session>  (one line per injected fact,   │
     skipped for a slug already logged this session — a         │
     compact/clear re-render never inflates ref_count)           │
   write per-session injected-set (.runtime/injected-<sid>.list)│
   source=compact: rank against a bounded query built from the  │
     transcript's latest isCompactSummary record (focus         │
     sections, top-40 terms), not the empty prompt; source=clear│
     re-ranks against the empty prompt. Either way the emitted  │
     set is NOT filtered against what was already injected      │
     earlier in the session — the model's context was just      │
     wiped, so re-showing already-seen facts is the point.       │
                                                                │
UserPromptSubmit ──► inject --event=UserPromptSubmit ───────────┤
   source ∈ {system, poll_event}: exit 0, no output, no WAL,    │
     session list untouched (machine-injected turn, not a       │
     user prompt — ranking it would waste a fact's once-per-    │
     session slot)                                              │
   else: same ranker; keywords += prompt tokens (reactive)      │
   inject only facts newly entering the top-8 this session      │
   WAL: inject|<slug>|<session>                                 │
                                                                │
PreToolUse(Write|Edit) ──► guard ──────────────────────────────┤
   guarded: legacy ~/.claude/memory, this session's             │
     resolved store (autoMemoryDirectory/env-override           │
     included), native projects/<slug>/memory/ under            │
     both the config dir and ~/.claude, and memory-global/      │
   secrets.Scan(candidate strings): credential outside a fenced │
     code block → exit 2 + stderr (blocks the write)            │
   .secretsignore glob or HYPOMNEMA_ALLOW_SECRETS=1 → exit 0    │
   scanner error → fail-open (exit 0)                           │
                                                                │
PreToolUse(Skill) ──► skill-active ────────────────────────────┤
   record activated skill name in .runtime/ (so PostToolUse can │
     tag skill-learning facts to the right skill)               │
                                                                │
PostToolUse(Skill) ──► skill-inject ───────────────────────────┤
   inject skill-learning facts for the just-activated skill     │
   WAL: recall|<slug>|<session>  (joins the injected-set)       │
                                                                │
Stop ──► close ────────────────────────────────────────────────┤
   read the session transcript (JSONL) for assistant text       │
   closer.Classify(injected-set): evidence phrase / name cite   │
     hit → trigger-useful ; miss → trigger-silent               │
     (skipped entirely if the transcript was unreadable —       │
      never fabricate silent evidence for a whole session)      │
   WAL: trigger-useful|<slug>, trigger-silent|<slug>            │
   WAL: session-metrics (error_count/tool_calls/duration)       │
   WAL: session-close|<sid>                                     │
   sidecar.Reproject: ref_count, effectiveness,                 │
     last_injected, last_useful                                 │
   sidecar.MarkStale: decay by age from last-injection          │
   memindex.Write: regenerate this project's native MEMORY.md   │
   profile.Generate: self-profile.md                            │
   ── no native content is ever mutated by a hook ──────────────┘
```

### Dedup (CLI verb, not a default hook)

`memoryctl dedup check <file>` fuzzy-compares a proposed `mistake` file's
`root-cause` against existing mistakes (`internal/dedup` + `internal/fuzzy`,
a pure-Go rapidfuzz token-set port). At/above the merge threshold it blocks
(pre-tool) or merges into the existing file's `recurrence` and deletes the new
one (post-tool); a lower candidate threshold emits an advisory note. It emits
`dedup-blocked` / `dedup-merged` / `dedup-candidate`. It is a real verb but is
**not** wired into the six installed hooks — invoke it explicitly.

## The relevance ranker (one pipeline)

v2 merged v1's two scoring systems (composite-score `SessionStart` +
substring-trigger `UserPromptSubmit` with ±40-char negation windows) into a
single pure ranker in `internal/rank`. The authoritative formula lives in
`CLAUDE.md`; reproduced here:

```
score = 3.0 × overlap(session_keywords, file keywords+name+description+body)
      + 1.0 × log10(1 + ref_count) × effGate   # popularity, gated by usefulness
      + 2.0 × recency                  # 1/(1 + days/30) from last USEFUL citation (trigger-useful; fallback created)
      + 2.0 × effectiveness            # Bayesian (pos+1)/(pos+neg+2); neutral 0.5 until signal
      + 1.0 if project-local           # project facts outrank global on ties

effGate = clamp(2 × effectiveness, 0, 1)   # 1.0 at the prior (0.5); only damps, never amplifies
```

**Additive and zero-safe.** No single zero signal annihilates a candidate — a
brand-new fact with `ref_count=0` and no outcomes gets the neutral effectiveness
prior (0.5) and its frontmatter `created` as recency, so it is injectable from
day one. The `effGate` term scales `ref_count` by proven usefulness so a fact
injected hundreds of times that rarely helped cannot coast on volume; it is
neutral at the prior and capped at 1.0, so it only *damps* unearned popularity.

**Filters:** `status ∈ {active, pinned}` (sidecar-managed `stale` excluded);
scope = current project's store + the global store only (other projects never
inject); result cap top-8, ≤8KB total, once per session per fact. `session_keywords`
come from prompt tokens, cwd basename, and git context (branch, changed
filenames, recent commit subjects) on both `SessionStart` and `UserPromptSubmit`.

Pull retrieval (`memoryctl recall "<query>"`) runs the same ranker on an
explicit query, includes `stale` facts (marked `[stale]`, reviving on recall),
and joins the result into the session's injected-set for `close` to classify.

## Stores

### Store resolution

`memoryctl` resolves the per-project native store the same way Claude Code
itself resolves `autoMemoryDirectory`: an explicit override, else
`<config dir>/projects/<sanitized canonical root>/memory` — see
`docs/CONFIGURATION.md` § Store resolution for the full precedence
(`CLAUDE_COWORK_MEMORY_PATH_OVERRIDE` → `autoMemoryDirectory` → the default
path) and the project-anchor chain (`CLAUDE_PROJECT_DIR` →
`CLAUDE_PROJECT_CWD` → session pin → cwd) that feeds the canonical root.

**Canonical root and linked worktrees.** The canonical root is the nearest
git root above the project anchor. When that root is a *linked worktree*
(`.git` is a file, not a directory), it maps to the main checkout only when
git's own on-disk structure checks out end to end:

1. `<worktree>/.git` parses as `gitdir: <p>`.
2. `<p>/gitdir` exists and, resolved, names `<worktree>/.git` — the
   back-link git itself writes into every linked worktree's
   `<main>/.git/worktrees/<name>/gitdir`.
3. `<p>/commondir` resolves to a directory that exists, sits exactly two
   levels above `<p>` (the `../..` every git-created `commondir` holds for a
   linked worktree), and contains both `HEAD` and `objects/` — evidence a
   plain ancestor directory never has.
4. Every one of those pointer files is read capped at 4 KiB; oversized or
   unreadable counts as absent.

Any failure at any step keeps the worktree's own store — it never falls back
to a partially-trusted guess. This exists because a *planted* pointer file
(no real git required — a tarball/zip extraction can create a `.git` file
naming `gitdir: /` or an unrelated pre-existing repo) must not redirect a
session's memory reads/writes onto another project's store. `internal/native`
(`CanonicalRoot`, `mainCheckout`) is the only code that implements this;
`internal/doctor`'s `store_resolution` check (`memoryctl doctor`) surfaces
the resolved store and anchor for a live session so this is verifiable
without reading Go.

Content lives in **native memory files** — flat markdown with YAML frontmatter,
one logical `type:` field, no subdirectories:

| Location | Scope | Owner |
|---|---|---|
| `~/.claude/projects/<slug>/memory/` | per-project | Claude Code harness (native) |
| `~/.claude/memory-global/` | global (applies in every project) | hypomnema (native-format) |

Native memory is per-project only; the global store is the structural piece
hypomnema adds. `inject` unions both at runtime.

hypomnema's own metadata and runtime live under `~/.claude/memory/`:

| Path | Role |
|---|---|
| `.wal` | Append-only text event log — **the source of truth** |
| `.sidecar.db` | SQLite projection (metadata: ref_count, effectiveness, status, keywords). **Rebuildable** from `.wal` + native frontmatter |
| `self-profile.md` | Precision report regenerated on `close` |
| `.runtime/` | Per-session injected-set lists, active-skill marker |

The sidecar is a cache, not a replica: delete it and `memoryctl sidecar rebuild`
(or the next `close`) reprojects it from the WAL plus a native-frontmatter scan.
`content_sha` relinks a row when a file is edited or renamed so a fact's
identity — and its ref_count/effectiveness history — survives the change.

## Go packages (`internal/`)

One package, one responsibility. `memoryctl` (in `cmd/memoryctl/`) wires them.

| Package | Responsibility |
|---|---|
| `native` | Native-store adapter — the only code that knows the native format: enumerate/parse files, resolve the project memory dir from the anchor chain (`CLAUDE_PROJECT_DIR` → `CLAUDE_PROJECT_CWD` → session pin → cwd) via the canonical git root and the harness sanitizer — cwd is only the last-resort anchor, not the resolution rule itself (§ Store resolution). Content is **read-only** |
| `sidecar` | SQLite projection — schema, upserts, reproject, rank queries, `MarkStale`. Only place with SQLite |
| `wal` | Append-only event log; `Append`/`AppendStrict`, `SanitizeField`, lock acquisition. Four-column invariant enforced |
| `rank` | The pure relevance ranker (formula above). No I/O, no storage imports → trivially unit-testable and A/B-able |
| `inject` | Orchestrator: keywords → `native.List` → `sidecar` → `rank.TopK` → JSON |
| `closer` | Stop path: transcript classify → WAL → reproject/decay → profile |
| `dedup` + `fuzzy` | Fuzzy dedup on write (token-set ratio) |
| `secrets` | Secrets gate (credential patterns, `.secretsignore`) |
| `tokenize` | Unicode-aware tokenizer (salvaged from v1's TF-IDF; Cyrillic/CJK/Greek participate, not just Latin) |
| `profile` | `self-profile.md` generation from WAL aggregation |
| `doctor` | Health check (`memoryctl doctor`) |
| `migrate` | One-shot v1 → v2 conversion + pruning |
| `memindex` | Renders the native `MEMORY.md` index (flat slug links) |
| `pathutil` | Shared slug/filename sanitisers |
| `ab` | Offline A/B harness: replay historical WAL, ranked-top-K vs dump-all on `trigger-useful` proxy |
| `invariants` | Automated checks for the mechanically-checkable rules in `docs/INVARIANTS.md` |
| `jsonl` | Streams the session-transcript JSONL, extracting assistant-authored text for evidence classification |

### `memoryctl` command surface

`inject`, `close`, `guard`, `recall`, `skill-inject`, `skill-active` (hook hot
paths); `dedup check`, `migrate`, `doctor`, `self-profile`, `sidecar
rebuild|show`, `wal`, `audit`, `rank`, `reindex`, `ab` (manual / maintenance).

## Invariants (code-level contracts)

The full list with rationale is `docs/INVARIANTS.md`; `internal/invariants`
mechanically checks the ones that can be. The load-bearing ones:

1. **Native content is agent-owned; hooks are read-only on it.** No hook ever
   edits a memory file's body or frontmatter. The one hook-managed native file
   is a project's `MEMORY.md` index, regenerated wholesale on `close`.
2. **Write-by-agent, read-by-hook.** Agents create memory files via the Write
   tool; hooks only react to existing files.
3. **Append-only WAL.** Never rewritten; corrections are new events.
4. **Four-column WAL.** `date|event|target|session`; pipes/newlines in source
   data are replaced with `_` before write (`wal.SanitizeField`).
5. **Sidecar is derivable.** State is reconstructable from WAL + native
   frontmatter; the WAL is authoritative, the DB is a cache.
6. **No network.** Zero HTTP/curl anywhere in `internal/`, `cmd/`, or the
   shims. Hypomnema is entirely offline.
7. **Silent-fail.** A hook never breaks the session: any internal error in
   `inject`/`close` exits 0. The *only* non-zero exit is `guard` on a detected
   secret (`exit 2`); even a scanner crash fails open.

## Secrets gate

`memoryctl guard` (PreToolUse `Write|Edit`) scans the candidate write content
for credential patterns outside fenced code blocks: key-based
(`api_key`/`secret`/`password`/`token` + a ≥8-char value) and value-based (AWS
`AKIA…`/`ASIA…`, GitHub `ghp_…`/`github_pat_…`, Anthropic `sk-ant-…`, Slack
`xox*-…`, PEM private keys, JWTs, `scheme://user:pass@` URLs). A hit exits 2
with a stderr line naming the file. Escape hatches: a glob in
`~/.claude/memory-global/.secretsignore`, or `HYPOMNEMA_ALLOW_SECRETS=1` for a
single invocation.

## Retired in v1 (see git tag `v1.1.2`)

A reader coming from v1 docs will look for these — all removed in v2, verify by
`ls`/grep rather than assuming they exist:

- **Five 700-line bash hooks + `hooks/lib/` libraries** → six ~5-line shims + Go.
- **Two scoring pipelines** (composite-score + priority-key) → one `rank`.
- **Substring triggers + ±40-char negation windows** → tokens are relevance signal.
- **FTS5 shadow retrieval** (`internal/fts`, `bin/memory-fts-*.sh`, `shadow-miss`) → gone.
- **TF-IDF body scoring / cold-start gates** → gone (Unicode tokenizer salvaged into `tokenize`).
- **`.config.sh` safe-parser, `projects.json` longest-prefix detection** → project resolved from the anchor chain (§ Store resolution), cwd only as the last resort.
- **`_agent_context.md`** subagent file → pass facts inline in the subagent prompt.
- **`PreCompact` nudge hook, per-type quotas (3+3/12/10/8), rotation to `archive/`** → decay is down-rank-in-sidecar; balance emerges from relevance.
- **`scripts/parity-check.sh` bash↔Go parity contract** → Go is the single implementation.

## Cross-references

- Agent protocol, frontmatter schema, ranker formula, writing rules — `CLAUDE.md` (repo root).
- v2 design rationale — `docs/specs/2026-05-28-v2-native-memory-design.md`.
- WAL event registry — `docs/EVENTS.md`.
- WAL grammar — `docs/FORMAT.md`.
- Invariant list — `docs/INVARIANTS.md`.
- Hook contract surface (inputs, outputs, exit codes) — `docs/hooks-contract.md`.
- Tunable parameters — `docs/CONFIGURATION.md`.
- v1.x → v2 migration — `docs/MIGRATION.md`.
- A/B ranker evidence — `docs/measurements/2026-05-29-v2-ranker-ab.md`.
