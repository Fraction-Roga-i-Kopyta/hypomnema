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

`install.sh` copies nine shims into `~/.claude/hooks/v2/` and registers them in
`settings.json`. Each shim resolves `memoryctl` (fail-open: `exit 0` if the
binary is absent) and `exec`s one verb, forwarding stdin (the hook envelope)
and preserving the exit code. The exceptions do not `exec`, so an older
binary that lacks their verb stays a no-op: the two subagent shims always
exit 0, and `pre-tool-bash.sh` blocks (exit 2) only when `guard-bash`'s
stderr starts with `BLOCKED (hypomnema)`.

| Event | Matcher | Shim | Verb | Purpose |
|---|---|---|---|---|
| `SessionStart` | — | `session-start.sh` | `inject --event=SessionStart` | Inject the fixed core (freshest continuity, pinned facts, project feedback) into `additionalContext` — no ranking; on `compact`/`clear` the same core without render-dedup |
| `UserPromptSubmit` | — | `user-prompt-submit.sh` | `inject --event=UserPromptSubmit` | Reactive re-rank with prompt tokens; inject newly-relevant facts |
| `PreToolUse` | `Write\|Edit` | `pre-tool-write.sh` | `guard` | Secrets gate on memory-path writes (`exit 2` blocks) |
| `PreToolUse` | `Bash` | `pre-tool-bash.sh` | `guard-bash` | Block the model's own `memoryctl pin`/`unpin`/`mark` (`mark … done` passes) — `/memboard` panel actions; stops accidental or injected CLI calls, not a security boundary |
| `PreToolUse` | `Skill` | `skill-active.sh` | `skill-active` | Record the activated skill for this session |
| `PostToolUse` | `Skill` | `skill-learnings-inject.sh` | `skill-inject` | Inject accumulated skill-learning facts for that skill |
| `Stop` | — | `session-stop.sh` | `close` | Classify citations → WAL → effectiveness/decay → `MEMORY.md` |
| `SubagentStart` | — | `subagent-start.sh` | `inject --event=SubagentStart` | Gate on the Agent call's description, rank the passing facts, inject its own top-5 under its own key |
| `SubagentStop` | — | `subagent-stop.sh` | `close --subagent` | Classify the subagent's own citations (incl. its hand-back report) under its own key |

Compaction is handled on `SessionStart` with `source=compact` (and `/clear`
with `source=clear`), not by a separate hook: **no `PreCompact`/`PostCompact`
hook is registered**. `PreCompact` was retired with v1. Claude Code does have
a `PostCompact` event (fields `trigger`, `compact_summary`; no
`additionalContext`), but it carries no way to reach the model's context —
`SessionStart` with `source=compact` is the event whose `additionalContext`
actually gets seen, so that is where hypomnema re-renders the core instead. The
shims carry zero logic; everything below happens inside `memoryctl`.

## End-to-end data flow

```
SessionStart ──► memoryctl inject --event=SessionStart ─────────┐
   resolveStore(cwd) → project store + global store             │
   native.List(project store + global store)                    │
   sidecar reproject if content_sha / mtime drift               │
   no query terms (no git calls, no overlap query): no ranking  │
   core = coreFacts(...)  ── fixed, not ranked:                 │
     1. the project's freshest continuity (created/modified)    │
     2. status: pinned — project-local, then global             │
     3. project-local type: feedback                            │
     freshest first per group; stale/retired, /memboard hide or │
     less → not core; one fact per slug, project-local wins     │
   emit the core (≤2.5KB/body, ≤8KB total, Memory-queue line    │
     reserved first) as `# Memory Context` in additionalContext;│
     core bodies that do not fit → one-line pointers (not       │
     deliveries, no WAL row)                                    │
   WAL: inject|<slug>|<session>  (one line per injected fact,   │
     skipped for a slug already logged this session — a         │
     compact/clear re-render never inflates ref_count)           │
   write per-session injected-set (.runtime/injected-<sid>.list)│
   source=compact|clear: the same core, NOT filtered against    │
     what was already injected earlier in the session — the     │
     model's context was just wiped, so re-showing the core is  │
     the point. Nothing ranked follows it after compaction: the │
     summary carries the task state.                            │
                                                                │
UserPromptSubmit ──► inject --event=UserPromptSubmit ───────────┤
   source ∈ {system, poll_event}: exit 0, no output, no WAL,    │
     session list untouched (machine-injected turn, not a       │
     user prompt — ranking it would waste a fact's once-per-    │
     session slot); the same for the /memboard toggle           │
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
   closer.Citations(assistant text): <cc-memory> tags           │
     cited + delivered → cite-useful ; cited, undelivered       │
       → cite-undelivered ; uncited, session had ≥1             │
       delivered cite → cite-silent ; no delivered citation     │
       this session → cite-none                                 │
     (skipped entirely if the transcript was unreadable —       │
      never fabricate silent evidence for a whole session)      │
   WAL: cite-useful|<slug>, cite-silent|<slug>,                 │
     cite-undelivered|<slug>, cite-none|<sid>                   │
   WAL: session-close|<sid>                                     │
   sidecar.Reproject: ref_count, effectiveness,                 │
     last_injected, last_useful                                 │
   sidecar.MarkStale: decay by age from last-injection          │
     (exempt: pinned, each project's freshest continuity,       │
     project-local feedback — the SessionStart core, refreshed  │
     only by being core)                                        │
   memindex.Write: regenerate this project's native MEMORY.md   │
   ── no native content is ever mutated by a hook ──────────────┤
                                                                │
SubagentStart ──► inject --event=SubagentStart ─────────────────┤
   session_id/transcript_path are the PARENT's; agent_id/type   │
     name the subagent                                          │
   agent_type in skip list (default fork/Explore/claude-code-   │
     guide/statusline-setup, override HYPOMNEMA_SUBAGENT_SKIP)  │
     → exit 0, nothing written                                  │
   key = session_id + ":" + agent_id                            │
   claim rendered-<key>.list (O_CREATE|O_EXCL) before ranking:  │
     already claimed (resumed agent / concurrent duplicate)     │
     → exit 0, nothing written                                  │
   pickAgentCall: jsonl.AgentCalls(parent transcript tail       │
     ≤2MiB, agent_type, agent_id) → own Agent/Task call, else   │
     the oldest unclaimed pending call of this type             │
     no call → nothing injected (no parent-prompt fallback)     │
   brief = the call's description; query = description +       │
     prompt (top-40 terms) + type words, NoGitSignal=true       │
   head gate BEFORE rank: ≥2 distinct brief terms in the head   │
     (name/description/keywords/domains), one in name or        │
     keywords; paths, $HOME/project-root words, type words,     │
     digits, corpus-common head terms never count               │
   rank the passing facts, MaxK=5, MaxBytes=5000 → emit         │
   WAL: inject|<slug>|<key>                                     │
   write injected-<key>.list (if non-empty), rendered-<key>.list│
     (always — marks the claim as fulfilled)                    │
                                                                │
SubagentStop ──► close --subagent ──────────────────────────────┤
   agent_transcript_path = the SUBAGENT's own transcript        │
   agent_type in skip list, or any of session_id/agent_id/      │
     agent_transcript_path empty → exit 0, nothing written      │
   closer.Run(SessionID=key, TranscriptPath=agent_transcript_   │
     path, Subagent=true): same citation classification as      │
     close, jsonl folds SubagentHandback.input.message into the │
     transcript text first                                      │
   WAL: cite-useful|<slug>|<key>, cite-silent/cite-undelivered, │
     cite-none|<key>|<key> as usual — no sidecar reproject/     │
     MarkStale, no MEMORY.md regen (left to the parent's Stop)  │
   WAL: session-close|<key>|<key>, deduped END-ANCHORED         │
     (wal.AppendSuffixUnique) so it never suppresses — or is    │
     suppressed by — the parent's own session-close|<sid>|<sid> │
   unreadable subagent transcript: session-close only, no       │
     cite-* row for this key                                    │
   ── session-level work stays with the parent's next Stop ─────┘
```

## The relevance ranker (one pipeline)

v2 merged v1's two scoring systems (composite-score `SessionStart` +
substring-trigger `UserPromptSubmit` with ±40-char negation windows) into a
single pure ranker in `internal/rank`. The authoritative formula lives in
`CLAUDE.md`; reproduced here:

It serves UserPromptSubmit, SubagentStart (after its head gate), `recall`
and `skill-inject`; SessionStart does not rank (it injects the fixed core,
see the data flow above and invariant I6).

```
score = 3.0 × overlap(session_keywords, file keywords+name+description+body)
      + 2.0 × recency                  # 1/(1 + days/30) from last USEFUL citation (cite-useful; fallback created)
      + 1.0 if project-local           # project facts outrank global on ties
```

A `/memboard` *less* mark halves the score; *hide* drops the fact.
`ref_count` and `effectiveness` are not ranking inputs — both are circular
(injection volume feeds `ref_count`; `cite-silent`, a consequence of being
injected, feeds `effectiveness`). `close` still computes them as diagnostics
for `/memboard`, `stats`, `doctor` and `sidecar show`. See ADR
[`session-start-core`](decisions/session-start-core.md).

**Additive and zero-safe.** No single zero signal annihilates a candidate — a
brand-new fact with no citation history gets its frontmatter `created` as
recency (the date of Claude Code's `modified` stamp when `created` is
absent), so it is injectable from day one.

**Filters:** `status ∈ {active, pinned, candidate}` (sidecar-managed `stale`
excluded; `recall` includes it); scope = current project's store + the global
store only (other projects never inject); result cap top-8 (top-5 for a
subagent), ≤8KB total, once per context per fact (again after
compaction/clear). `session_keywords` come from prompt tokens, cwd basename,
and git context (branch, changed filenames, recent commit subjects) on
`UserPromptSubmit`; a subagent's come from its Agent call, without git
context.

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
| `.wal.pre-compact-<date>` | The pre-compaction WAL that `memoryctl wal compact` keeps; delete it once you no longer need it |
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
| `wal` | Append-only event log; `Append`/`AppendStrict`, `SanitizeField`, lock acquisition, `Validate`, `Compact`. Four-column invariant enforced |
| `rank` | The pure relevance ranker (formula above). No I/O, no storage imports → trivially unit-testable and A/B-able |
| `inject` | Orchestrator: `native.List` → `sidecar` → SessionStart core (`coreFacts`) or keywords → head/prompt gate → `rank.Rank` top-K → JSON |
| `closer` | Stop path: transcript classify → WAL → reproject/decay → `MEMORY.md`; `Subagent: true` runs the same classification under a subagent's own key and skips the session-level steps |
| `secrets` | Secrets gate (credential patterns, `.secretsignore`) |
| `tokenize` | Unicode-aware tokenizer (salvaged from v1's TF-IDF; Cyrillic/CJK/Greek participate, not just Latin) |
| `doctor` | Health check (`memoryctl doctor`) |
| `migrate` | One-shot v1 → v2 conversion + pruning |
| `memindex` | Renders the native `MEMORY.md` index (flat slug links) |
| `pathutil` | Shared slug/filename sanitisers |
| `ab` | Offline A/B harness: replay historical WAL, ranked-top-K vs dump-all on `cite-useful` proxy |
| `invariants` | Automated checks for the mechanically-checkable rules in `docs/INVARIANTS.md` |
| `jsonl` | Streams the session-transcript JSONL, extracting assistant-authored text and Read-tool paths for citation classification (folding in `SubagentHandback` text); `AgentCalls` reads a parent transcript's tail for the Agent call that launched a subagent (its description is the head-gate brief, description + prompt the ranking query) |

### `memoryctl` command surface

`inject`, `close`, `guard`, `recall`, `skill-inject`, `skill-active` (hook hot
paths); `migrate`, `doctor`, `retire`, `revive`, `sidecar rebuild|show`,
`wal validate|compact`, `audit`, `rank`, `reindex`, `ab` (manual / maintenance).

## Invariants (code-level contracts)

The full list with rationale is `docs/INVARIANTS.md`; `internal/invariants`
mechanically checks the ones that can be. The load-bearing ones:

1. **Native content is agent-owned; hooks are read-only on it.** No hook ever
   edits a memory file's body or frontmatter. The one hook-managed native file
   is a project's `MEMORY.md` index, regenerated wholesale on `close`.
2. **Write-by-agent, read-by-hook.** Agents create memory files via the Write
   tool; hooks only react to existing files.
3. **Append-only WAL.** Hooks only append; corrections are new events. The
   one rewrite is the manual `memoryctl wal compact`, which drops only event
   types no reader consumes and keeps the original as `.wal.pre-compact-<date>`.
4. **Four-column WAL.** `date|event|target|session`; pipes/newlines in source
   data are replaced with `_` before write (`wal.SanitizeField`).
5. **Sidecar is derivable.** State is reconstructable from WAL + native
   frontmatter; the WAL is authoritative, the DB is a cache.
6. **No network.** Zero HTTP/curl anywhere in `internal/`, `cmd/`, or the
   shims. Hypomnema is entirely offline.
7. **Silent-fail.** A hook never breaks the session: any internal error in
   hook verb exits 0, and a panic is recovered (`runHook`, stderr trace, exit
   0). The *only* non-zero exit is `guard`/`guard-bash` on a detected secret
   or panel verb (`exit 2`); even a scanner crash fails open.

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

- **Five 700-line bash hooks + `hooks/lib/` libraries** → nine shims of a few lines + Go.
- **Two scoring pipelines** (composite-score + priority-key) → one `rank`.
- **Substring triggers + ±40-char negation windows** → tokens are relevance signal.
- **FTS5 shadow retrieval** (`internal/fts`, `bin/memory-fts-*.sh`, `shadow-miss`) → gone.
- **TF-IDF body scoring / cold-start gates** → gone (Unicode tokenizer salvaged into `tokenize`).
- **`.config.sh` safe-parser, `projects.json` longest-prefix detection** → project resolved from the anchor chain (§ Store resolution), cwd only as the last resort.
- **`_agent_context.md`** subagent file → `SubagentStart` ranks and injects the subagent's own top-5 automatically; pass inline only what is specific to the task beyond that.
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
