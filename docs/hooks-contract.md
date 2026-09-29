# Hypomnema hook contract — v2

**Status:** STABLE. Companion to `docs/FORMAT.md`.

This document describes the wire shape between Claude Code and the hypomnema
hooks. It covers the six hooks the installer wires, the stdin/stdout JSON
shapes, exit codes, environment variables, and the fail-safe rules.

In v2 every hook is a **thin shim** (`hooks/v2/*.sh`, a few lines of `sh`) that
does one thing: locate the `memoryctl` binary and `exec` it with a verb, piping
the Claude Code hook envelope straight through on stdin. All logic lives in
`memoryctl` (`cmd/memoryctl/` + `internal/`). `memoryctl` is **mandatory** — it
is not an optional dependency. The shim's only defensive behaviour is that if
the binary is missing from its resolved path it exits 0 (so a broken install
never breaks a session); it does not degrade to a bash fallback.

The invariants here are what `install.sh` wires into `~/.claude/settings.json`.
This document describes **hypomnema's own commitments**, not the upstream Claude
Code envelope (new event types / tool-input fields are tracked separately).

---

## 1. Shared contract

### 1.1 The six hooks

`install.sh` (`register_hook` calls) wires exactly these, and nothing else:

| Event | Matcher | Shim | `memoryctl` verb | Timeout | Purpose |
|---|---|---|---|:-:|---|
| `SessionStart` | — | `session-start.sh` | `inject --event=SessionStart` | 15 s | Rank + inject memory at session open |
| `UserPromptSubmit` | — | `user-prompt-submit.sh` | `inject --event=UserPromptSubmit` | 10 s | Re-rank + inject for the just-typed prompt |
| `PreToolUse` | `Write\|Edit` | `pre-tool-write.sh` | `guard` | 10 s | Secrets gate — block credential writes |
| `PreToolUse` | `Skill` | `skill-active.sh` | `skill-active` | 10 s | Record the activated skill for this session |
| `PostToolUse` | `Skill` | `skill-learnings-inject.sh` | `skill-inject` | 10 s | Inject skill-learning facts for the skill |
| `Stop` | — | `session-stop.sh` | `close` | 10 s | Close the session: classify, decay, rollup |

Compaction is handled on `SessionStart` with `source=compact`; **no
`PreCompact`/`PostCompact` hook is registered**. There is also no
fuzzy-dedup `PreToolUse` hook and no `PostToolUse` outcome/error-detect
hooks. Those v1 mechanics are retired (see §9).

### 1.2 Input

Every verb reads a single JSON object from stdin (the Claude Code hook
envelope) and consumes only a known subset, ignoring unknown keys. The fields
each verb actually reads:

| Verb | Fields read from stdin |
|---|---|
| `inject` | `session_id`, `cwd`, `prompt`, `source` (routes SessionStart's compact/clear re-render and UserPromptSubmit's system/poll_event skip), `transcript_path` (SessionStart only, to find the latest compaction summary when `source=compact`) |
| `guard` | `cwd` (resolves the store this invocation guards), `tool_input.file_path`, `tool_input.content` (Write), `tool_input.new_string` (Edit), `tool_input.new_source` (NotebookEdit), `tool_input.edits[].new_string` (MultiEdit) |
| `skill-active` | `session_id`, `tool_input.skill` |
| `skill-inject` | `session_id`, `cwd` (resolves the store the learnings are read from), `tool_input.skill` |
| `close` | `session_id`, `cwd`, `transcript_path` |

### 1.3 Output

The two `inject` verbs and `skill-inject` write a single JSON object on stdout
that Claude Code prepends to the model's context:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "SessionStart",
    "additionalContext": "# Memory Context\n\n## <name>\n<body>\n..."
  }
}
```

`hookEventName` is `SessionStart` or `UserPromptSubmit` for `inject`
(mirroring the `--event` flag). `additionalContext` is the ranked `# Memory
Context` block (top-8 facts, ≤2.5 KB per body, ≤8 KB total). Empty context is
valid and means nothing matched.

`guard`, `skill-active`, and `close` produce **no** stdout envelope. `guard`
communicates via exit code + stderr; `skill-active` and `close` are
side-effect-only (WAL writes, sidecar updates, `.runtime/` markers).

### 1.4 Exit codes

| Code | Meaning |
|---|---|
| `0` | Success or graceful no-op. Claude Code continues normally. |
| `2` | **`guard` only** — block the tool call. stderr is surfaced to Claude. |
| other | Undefined by hypomnema. |

**Fail-safe rule:** every verb exits `0` on bad or absent input — malformed
envelope JSON, unreadable memory, missing session id. A broken memory file is
never a reason to break the user's session. The **only** intentional non-zero
exit is `2` from `guard` when it finds a secret. (`memoryctl dedup check`, a
manual CLI verb not wired to any hook, also uses exit 2, but no hook invokes
it.)

### 1.5 Environment variables

Read by `memoryctl` (from its `--help` usage) and by the shims:

| Variable | Purpose | Default |
|---|---|---|
| `HYPOMNEMA_MEMORYCTL` | Path the shim uses to locate the `memoryctl` binary. | `$HOME/.claude/bin/memoryctl` |
| `CLAUDE_MEMORY_DIR` | Runtime memory root (WAL, sidecar, `.runtime/`, self-profile). | `$HOME/.claude/memory` |
| `CLAUDE_PROJECT_DIR` | Project anchor Claude Code sets on every hook invocation; outranks `CLAUDE_PROJECT_CWD`. | (always set by Claude Code inside a hook) |
| `CLAUDE_PROJECT_CWD` | hypomnema-only explicit override for CLI use; ignored inside hooks — `CLAUDE_PROJECT_DIR` wins there. | current working dir |
| `CLAUDE_CONFIG_DIR` | Claude Code config dir used for native store resolution (`projects/`, `settings.json`) only; the global store stays at `~/.claude/memory-global`. | `$HOME/.claude` |
| `CLAUDE_COWORK_MEMORY_PATH_OVERRIDE` | Highest-precedence store override: an absolute path used verbatim as the resolved memory dir. | unset |
| `HYPOMNEMA_SESSION_ID` | Session id stamped into WAL entries. | `unknown` |
| `CLAUDE_CODE_SESSION_ID` | Session id Claude Code exports into Bash; `recall` falls back to it when `HYPOMNEMA_SESSION_ID` is unset, and it is how a CLI verb looks up the session pin written at `SessionStart`. | (inherited) |
| `HYPOMNEMA_TODAY` | Freeze "today" (`YYYY-MM-DD`) in date-sensitive logic; used by tests/replay. | `$(date +%Y-%m-%d)` |
| `HYPOMNEMA_NOW` | Freeze the self-profile `generated:` stamp (`YYYY-MM-DD HH:MM`). | now |
| `HYPOMNEMA_ALLOW_SECRETS` | Set to `1` to bypass the `guard` secrets gate for a single invocation. | unset |
| `CLAUDE_HOME` | Test/parallel-install override for hypomnema's own state root, and — checked before `CLAUDE_CONFIG_DIR` — for native store resolution too. | `$HOME/.claude` |

Implementations MAY add new variables. They MUST NOT re-purpose the ones above.

### 1.6 Side effects

Verbs may read and write:

- Native memory files: `~/.claude/projects/<slug>/memory/*.md` (per-project) and
  `~/.claude/memory-global/*.md` (global) — per FORMAT.md §3.
- The runtime tree `~/.claude/memory/`: `.wal` (§5 of FORMAT.md), `.sidecar.db`
  (the single derivative index), `self-profile.md`, and `.runtime/`
  (session-scoped markers: `injected-<session_id>.list` — the session's
  injected union, for `close` classification, WAL `inject`-row dedup, and
  as the render-dedup fallback when no rendered list exists yet;
  `rendered-<session_id>.list` — what is in the model's *current* context,
  the normative dedup source for the next render, reset on `compact`/`clear`;
  `holdout-<session_id>.list` — facts withheld for `ablate`; `active-skill-<sid>`
  — the marker `skill-active` writes; and the store-resolution session pin
  `project-<session_id>.json`, written by `inject` at `SessionStart` and read
  back by later CLI verbs in the same session via
  `CLAUDE_CODE_SESSION_ID`/`HYPOMNEMA_SESSION_ID`). The three `*.list` families
  plus the session pin are pruned after 7 days of inactivity (`inject`'s
  `pruneRuntimeLists`); `active-skill-<sid>` ages out separately and sooner —
  `close` removes it after 24 h (§5) — since a stale skill marker would
  mis-tag `skill-learning` captures long before a week is up.

Verbs MUST NOT modify `~/.claude/settings.json` at runtime, prompt
interactively (hooks run headless), or block past the configured timeout.

---

## 2. SessionStart — `inject --event=SessionStart`

**Timeout:** 15 s.

Reads `session_id`, `cwd`, `prompt` (usually empty at session open), `source`,
and `transcript_path`. Ranks the current project + global native memory
against `session_keywords` (prompt tokens, CWD basename, git branch / changed
files / recent commit subjects) and emits the top-8 as
`hookSpecificOutput.additionalContext` with `hookEventName: SessionStart`.

`source` distinguishes a fresh session from a re-render after the model's
context was wiped:

- `source=compact` or `source=clear` — nothing previously rendered survives
  in the model's context, so this render's dedup resets to nothing: any
  fact still in the top-8 is re-emitted regardless of what
  `.runtime/rendered-<session_id>.list` or the injected union hold. On
  `compact` specifically, ranking uses a bounded query built from the
  transcript's latest `isCompactSummary` record (focus sections, top-40
  terms) instead of the empty prompt, so the re-render tracks what the
  compaction summary is actually about. A slug already logged as `inject`
  for this session does **not** get a second WAL row on re-render —
  `ref_count` is not inflated by compaction.
- any other value (or absent, for the normal session-open path) — dedup
  against `.runtime/rendered-<session_id>.list`: the slugs currently in the
  model's context (this render's normative dedup source). A session whose
  rendered list does not exist yet — one that predates the file, or has not
  rendered anything this session — falls back to the injected-session-union
  `.runtime/injected-<session_id>.list`.

Contract:

- Exit 0 always; empty `additionalContext` when nothing matched.
- Writes an `inject` WAL event per **newly-logged** emitted fact (see above —
  a compact/clear re-render of an already-logged fact writes no second row).
  The sidecar bumps `ref_count` and recency from those events.
- Records the injected slugs in `.runtime/injected-<session_id>.list` — the
  session union used for WAL `inject`-row dedup and read by `close` to
  classify the whole session's citations — and unions the newly rendered
  slugs into `.runtime/rendered-<session_id>.list`, reset to exactly this
  render's output on compact/clear.

## 3. UserPromptSubmit — `inject --event=UserPromptSubmit`

**Timeout:** 10 s.

Same verb, `hookEventName: UserPromptSubmit`. Reads `session_id`, `cwd`,
`prompt`, and `source`.

`source=system` or `source=poll_event` marks a machine-injected turn — a task
notification, a peer/agent hand-back, an auto-continuation, a poll event —
not something the user typed. `inject` exits 0 immediately: no ranking, no
`additionalContext`, no WAL write, the injected-set list untouched. Ranking
that text would spend a fact's once-per-context slot on noise. A payload
without `source` (older Claude Code versions) is treated as a real prompt.

Otherwise, re-ranks against the just-typed `prompt` (folded into
`session_keywords`) and emits **only** facts that newly enter the top-8 —
anything already listed in `.runtime/rendered-<session_id>.list` (or, when
that file does not exist yet, the injected-session-union) is not re-emitted:
once per context, again after a compaction/clear wipes it. Exit 0 always;
`inject` WAL events for the newly injected facts.

There is no substring-trigger matching and no negation-token logic; all tokens
are relevance signal to a single ranker (see CLAUDE.md "How injection ranks
files"). Both are retired v1 mechanics.

`inject`, `recall`, and `skill-inject` each open their rendered
`additionalContext` with the citation instruction line, verbatim, exactly
once per delivery:

> When a fact below changes what you say or do, wrap that sentence in
> `<cc-memory filenames="FILE">…</cc-memory>` (the tag is hidden from the
> user).

"Hidden from the user" holds for the interactive CLI; headless invocation
(`claude -p`) or raw SDK output may surface the tag literally, since there
is no interactive renderer to strip it — the instruction line itself stays
unchanged regardless.

Past that line, each path renders its own header shape — but the file name
is always present in it, since the file name is the only thing the model can
put inside `filenames="…"` to cite that fact:

- **`inject`** (`internal/inject`, `FactHeader`) — one
  `## <name> — <file.md> (<type>, <created>)` header per injected fact, in
  ranked order, each followed by its body.
- **`recall`** (`cmd/memoryctl/recall.go`, `renderRecall`) — a hybrid
  render: the top match gets a full header, `## <name> — <file.md> (<type>,
  score N)` (plus a trailing `[stale]` mark when applicable), followed by
  its body; runner-ups are index-only numbered lines with no `##` header —
  `N. <file.md> (score)[stale][ — description]` followed by the file's
  absolute path on the next line.
- **`skill-inject`** (`cmd/memoryctl/skillinject.go`, `runSkillInject`) — a
  flat list, no `##` header at all: one `- (<file.md>) <body>` entry per
  delivered learning.

All three count inside the render budget (§1.3) like any other emitted
text.

## 4. PreToolUse `Write|Edit` — `guard` (secrets gate)

**Timeout:** 10 s. **The only hook that can block.**

Reads the mutating tool's payload from stdin and blocks (exit 2) a write into a
guarded memory store whose content carries a plaintext credential.

Guarded stores (from `guardedRel`): the runtime tree `$CLAUDE_MEMORY_DIR`, the
global store `~/.claude/memory-global/`, this invocation's resolved store
(including an `autoMemoryDirectory`/env-override store, which lives outside
`projects/`), and every native `projects/<slug>/memory/` store under both the
config dir and `~/.claude` (so the check still covers a `CLAUDE_CONFIG_DIR`
install's own tree even when `guard` itself resolves the other one). A write
anywhere else is not policed.

Scan input: `content` (Write), `new_string` (Edit), `new_source`
(NotebookEdit), and each `edits[].new_string` (MultiEdit) are concatenated and
scanned by `internal/secrets`.

Exit **0** (allow) when any of: envelope JSON won't parse; `HYPOMNEMA_ALLOW_SECRETS=1`;
`file_path` empty; path outside every guarded store; path matches a glob in a
`.secretsignore` (`~/.claude/memory-global/.secretsignore`); concatenated
content empty; no secret pattern hit.

Exit **2** (block) when `internal/secrets` matches. stderr carries:

```
Blocked: <file_path> would write a plaintext secret-looking token.
Matches (line : fragment):
  <line> : <fragment>
If intentional, set HYPOMNEMA_ALLOW_SECRETS=1 for this single invocation and retry.
```

Patterns (see CLAUDE.md "Secrets detection"): key-based (`api_key` / `secret` /
`password` / `token` + a ≥8-char value) plus value-based (AWS `AKIA…`/`ASIA…`,
GitHub `ghp_…`/`github_pat_…`, Anthropic `sk-ant-…`, Slack `xox*-…`, PEM
blocks, JWTs, `scheme://user:pass@` URLs). An unclosed code fence does not
exempt the rest of the file.

## 5. PreToolUse `Skill` — `skill-active`

**Timeout:** 10 s.

Reads `session_id` and `tool_input.skill`, and writes an active-skill marker
`$CLAUDE_MEMORY_DIR/.runtime/active-skill-<session_id>`. This lets the capture
path tag `skill-learning` facts with the correct skill even after context
compaction. Exit 0 always (including on missing skill/session). No stdout, no
WAL write. Stale markers (>24 h) are cleaned up by `close`.

## 6. PostToolUse `Skill` — `skill-inject`

**Timeout:** 10 s.

Reads `tool_input.skill`, retrieves the `skill-learning` facts bound to that
skill (frontmatter `skill: <name>`), and emits them as
`hookSpecificOutput.additionalContext`. Writes a `recall` WAL event per
delivered learning (so the close hook classifies them like any injected fact).
Exit 0 always — silent no-op on bad input, missing skill, or no matching
learnings.

## 7. Stop — `close`

**Timeout:** 10 s.

Reads `session_id`, `cwd`, `transcript_path` (tolerates its absence). Runs the
session-close pass and exits 0 with **no** stdout envelope (side-effect only —
it does not print a checkpoint reminder; that v1 behaviour is retired).

Contract (see CLAUDE.md "Lifecycle"):

- Classifies the session's injected set from explicit `<cc-memory
  filenames="…">` citations in the assistant transcript: a cited in-scope
  fact that was actually **delivered** this session (injected, recalled,
  skill-injected, or read directly with the Read tool) is `cite-useful`; a
  cited in-scope fact that was never delivered is `cite-undelivered`
  instead — diagnostic only, not useful, does not arm the guard below and
  does not confirm a candidate. An injected-but-uncited fact is
  `cite-silent`, but only in a session with at least one resolvable
  **delivered** citation — a session that resolved zero delivered
  citations (nothing cited, or every citation was `cite-undelivered`)
  writes one session-level `cite-none` instead, so a broken citation
  channel never fabricates a session of silent facts. Frontmatter
  `evidence:`/name substring matching no longer produces `cite-*`; it now
  feeds only the `ablate` holdout observation (`holdout-hit`/`holdout-miss`).
- Writes a `session-metrics` (v2 shape) and a `session-close` row per turn
  (Stop fires once per turn, not once per session). The per-fact
  classification rows — `cite-useful`/`cite-silent`,
  `holdout-hit`/`holdout-miss` — are written once per (fact, session).
- Recomputes effectiveness in the sidecar; marks unused facts `stale` past their
  type threshold (age from last injection). No native content is mutated;
  `pinned` / `continuity` / `project` facts never decay.
- Ages out stale `active-skill-<sid>` markers (>24 h) from `.runtime/`.

**Continuity files are agent-written in v2**, not generated here. When you stop
mid-task you write a `type: continuity` file into the project's native store
yourself (CLAUDE.md "When to write `continuity`"). The Stop hook does not touch
them.

---

## 8. Running hooks manually

Each verb reads its JSON envelope on stdin; this is how tests and debugging
exercise them:

```bash
MCTL=~/.claude/bin/memoryctl

# SessionStart / UserPromptSubmit
echo '{"session_id":"test","cwd":"/tmp","prompt":"help me with X"}' \
  | CLAUDE_MEMORY_DIR=/tmp/test-mem "$MCTL" inject --event=UserPromptSubmit

# guard (secrets gate)
echo '{"tool_input":{"file_path":"'"$HOME"'/.claude/memory-global/x.md","content":"api_key = \"abcd12345678\""}}' \
  | "$MCTL" guard ; echo "exit=$?"   # 0 = allow, 2 = block

# Stop
echo '{"session_id":"test","cwd":"/tmp","transcript_path":"/tmp/t.jsonl"}' \
  | CLAUDE_MEMORY_DIR=/tmp/test-mem "$MCTL" close
```

Freeze time for replay/diagnostics with `HYPOMNEMA_TODAY=2026-04-10`.

Validate the WAL grammar (FORMAT.md §5) with `memoryctl wal validate`
(exit 0 clean, 1 failing rows, 2 WAL missing / misuse).

---

## 9. Retired in v2

These v1 hooks and behaviours are **gone** — do not implement or rely on them:

- `PreCompact` hook and its `systemMessage` reminder.
- Fuzzy-dedup `PreToolUse` (matcher `Write`). `memoryctl dedup check` survives as
  a manual CLI verb but is not wired to any hook.
- `PostToolUse` `memory-outcome.sh` and `memory-error-detect.sh`.
- Substring `trigger:`/`triggers:` matching and negation-token windows.
- FTS5 shadow retrieval / `shadow-miss`.
- Stop-generated continuity files, checkpoint reminders, and stale→archive
  rotation.
- The bash `~/.claude/hooks/memory-*.sh` scripts (replaced by `hooks/v2/*.sh`
  thin shims).

## 10. Versioning

This document versions together with `FORMAT.md`. Any change to a wire shape,
exit-code semantics, or the fail-safe rule of an existing hook is a breaking
change requiring a major bump of both. Adding a new hook or a new optional
environment variable is non-breaking.
