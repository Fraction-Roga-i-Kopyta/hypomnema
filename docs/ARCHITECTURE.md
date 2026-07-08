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
| `SessionStart` | — | `session-start.sh` | `inject --event=SessionStart` | Rank native facts, inject top-K into `additionalContext` |
| `UserPromptSubmit` | — | `user-prompt-submit.sh` | `inject --event=UserPromptSubmit` | Reactive re-rank with prompt tokens; inject newly-relevant facts |
| `PreToolUse` | `Write\|Edit` | `pre-tool-write.sh` | `guard` | Secrets gate on memory-path writes (`exit 2` blocks) |
| `PreToolUse` | `Skill` | `skill-active.sh` | `skill-active` | Record the activated skill for this session |
| `PostToolUse` | `Skill` | `skill-learnings-inject.sh` | `skill-inject` | Inject accumulated skill-learning facts for that skill |
| `Stop` | — | `session-stop.sh` | `close` | Attribute outcomes → WAL → effectiveness/decay → self-profile |

There is **no `PreCompact` hook** (retired with v1). The shims carry zero
logic; everything below happens inside `memoryctl`.

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
   WAL: inject|<slug>|<session>  (one line per injected fact)   │
   write per-session injected-set (.runtime/injected-<sid>.list)│
                                                                │
UserPromptSubmit ──► inject --event=UserPromptSubmit ───────────┤
   same ranker; keywords += prompt tokens (reactive relevance)  │
   inject only facts newly entering the top-8 this session      │
   WAL: inject|<slug>|<session>                                 │
                                                                │
PreToolUse(Write|Edit) ──► guard ──────────────────────────────┤
   only on memory paths (legacy ~/.claude/memory, every native  │
     projects/<slug>/memory/, and memory-global/)               │
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
   sidecar.Reproject: ref_count, effectiveness, last_injected   │
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
      + 2.0 × recency                  # 1/(1 + days/30) from last injection (fallback created)
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
| `native` | Native-store adapter — the only code that knows the native format: enumerate/parse files, resolve the project memory dir from cwd. Content is **read-only** |
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
- **`.config.sh` safe-parser, `projects.json` longest-prefix detection** → project derived from cwd.
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
