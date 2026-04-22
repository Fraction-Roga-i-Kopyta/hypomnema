# Hypomnema hook contract — v1.0

**Status:** STABLE. Companion to `docs/FORMAT.md`.

This document describes the wire shape between Claude Code and any hypomnema hook implementation. It covers stdin JSON shape, stdout JSON shape, exit codes, environment variables, and fail-safe rules for every hook type hypomnema installs.

If you are writing a hook implementation (bash, Go, Rust, whatever), conform to this document. If you are writing tests for hooks, use this document as the reference for what you can assume and what you must tolerate.

The invariants here are what hypomnema's installer wires into `~/.claude/settings.json`. Changes to the upstream Claude Code hook envelope (new event types, new tool-input fields) are tracked separately — this document describes **hypomnema's own commitments**, not Claude Code's.

---

## 1. Shared contract

### 1.1 Input

Every hook reads a single JSON object from stdin. The full envelope is defined by Claude Code; hypomnema only consumes a known subset and MUST ignore unknown keys forward-compatibly.

Minimal fields every hook SHOULD expect:

```json
{
  "session_id": "<string>",
  "cwd": "<absolute-path>"
}
```

Per-event extensions are listed in each hook's section below.

### 1.2 Output

Every hook that wants to feed context back to Claude writes a single JSON object on stdout:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "<EventName>",
    "additionalContext": "<string>"
  }
}
```

`hookEventName` MUST match the hook type exactly (`SessionStart`, `Stop`, `UserPromptSubmit`, `PreCompact`). PreToolUse and PostToolUse hooks do not produce context envelopes — they communicate via exit code and stderr (PreToolUse) or side effects on the WAL (PostToolUse).

A hook that has no context to contribute MUST either print an empty-context envelope (`additionalContext: ""`) or no output at all. It MUST NOT print partial / invalid JSON.

### 1.3 Exit codes

| Code | Meaning |
|---|---|
| `0` | Success or graceful no-op. Claude Code continues normally. |
| `2` | **PreToolUse only** — block the tool call. stderr is surfaced to Claude. |
| Anything else | Undefined by hypomnema. Claude Code MAY surface the error to the user. |

**Fail-safe rule:** Every hypomnema hook MUST exit `0` on internal failure. A broken memory file, an unreadable config, a malformed WAL line — none of these are a reason to break the user's session. Log internally (if anywhere), exit 0, move on. The only intentional non-zero exit is `2` from PreToolUse/dedup.

### 1.4 Environment variables

| Variable | Purpose | Default |
|---|---|---|
| `CLAUDE_MEMORY_DIR` | Override the memory root. Used in tests and multi-memory setups. | `$HOME/.claude/memory` |
| `HYPOMNEMA_TODAY` | Override "today" (YYYY-MM-DD) in any date-sensitive logic. Used in tests to freeze WAL fixture age. | `$(date +%Y-%m-%d)` |
| `HYPOMNEMA_SESSION_ID` | Session identifier passed to subprocesses (notably `bin/memory-dedup.py`). Mirrors the `session_id` field from stdin. | `unknown` |
| `LC_ALL` | Forced to `C` by hooks that call `awk`/`perl` with float printf — keeps decimal separator as `.` across locales. | (inherited) |

Implementations MAY add new environment variables. They MUST NOT re-purpose the variables listed above with new semantics.

### 1.5 Side effects

Hooks may read and write:

- `~/.claude/memory/**/*.md` (per §3 of FORMAT.md).
- `~/.claude/memory/.wal` (per §5 of FORMAT.md).
- `~/.claude/memory/continuity/<project>.md` (Stop hook only).
- `~/.claude/memory/.runtime/` (session-scoped temp state; may be cleaned at will).
- Derivative indices (`.tfidf-index`, `index.db`, `.analytics-report`) — see FORMAT.md §6.

Hooks MUST NOT:

- Modify `~/.claude/settings.json` at runtime.
- Write outside `~/.claude/memory/` or `/tmp`.
- Prompt the user interactively (hooks run headless; there's no tty guarantee).
- Block for more than the configured hook timeout (default 10–15 s depending on event).

---

## 2. SessionStart

**Wire name:** `SessionStart`
**Timeout:** 15 s (configurable in `settings.json`).
**Purpose:** Inject relevant memory into the session opener.

### 2.1 Input

```json
{
  "session_id": "<string>",
  "cwd": "<absolute-path>"
}
```

### 2.2 Output

```json
{
  "hookSpecificOutput": {
    "hookEventName": "SessionStart",
    "additionalContext": "# Memory Context\n\n**Active domains:** <...>\n\n## Active Mistakes\n\n### <slug> [<severity>, x<recurrence>]\n..."
  }
}
```

`additionalContext` is Markdown that Claude Code prepends to the session's system context. Section headings within it are part of hypomnema's format but not mandated by Claude Code. The minimum viable output is `"# Memory Context\n"` — anything less is a signal the implementation couldn't run.

### 2.3 Contract

- Exit 0 always.
- Empty `additionalContext` is valid (means: nothing matched / memory is empty).
- Writes `inject`, optionally `cluster-load`, `index-stale` events to WAL.
- Increments `ref_count` and refreshes `referenced:` on every injected file.
- Session-private file `.runtime/injected-<session_id>.list` lists slugs this hook injected, for later dedup by UserPromptSubmit. Created mode `0600`.

---

## 3. UserPromptSubmit

**Wire name:** `UserPromptSubmit`
**Timeout:** 10 s.
**Purpose:** Inject trigger-matched files for the specific prompt the user just typed.

### 3.1 Input

```json
{
  "session_id": "<string>",
  "prompt": "<user-text>",
  "cwd": "<absolute-path>"
}
```

`prompt` is the raw user message. Fenced code blocks, inline backticks, and blockquotes are stripped **inside the hook** before substring matching. Implementations MUST NOT trigger on text inside fences.

### 3.2 Output

```json
{
  "hookSpecificOutput": {
    "hookEventName": "UserPromptSubmit",
    "additionalContext": "## Triggered (from prompt)\n\n### <slug> (matched: \"<phrase>\")\n<body>\n..."
  }
}
```

Absent output means no triggers matched. The heading `## Triggered (from prompt)` is part of the hypomnema presentation format and MAY change between minor versions; implementations SHOULD keep consumer-facing Markdown stable within the section.

### 3.3 Contract

- Exit 0 always.
- Reads `.runtime/injected-<session_id>.list` to dedup against SessionStart's set. Files already injected this session are NOT re-emitted.
- Negation detection MUST honour the tokens listed in FORMAT.md §4 (feedback/strategy) — `не`, `без`, `уже`, `already`, `skip`, `ignore`, `without`, `игнор`, case-insensitive.
- Writes `trigger-match` events for matched files.
- MAY spawn `bin/memory-fts-shadow.sh` as a detached background process. The background process MUST be fail-safe (exit 0 silently on any error) and MUST NOT affect this hook's output.

---

## 4. PreToolUse / fuzzy dedup

**Wire name:** `PreToolUse`
**Matcher (hypomnema only wires one):** `Write`
**Timeout:** 10 s.
**Purpose:** Block creation of a `mistakes/*.md` that is too similar to an existing one.

### 4.1 Input

```json
{
  "session_id": "<string>",
  "tool_name": "Write",
  "tool_input": {
    "file_path": "<absolute-or-relative-path>",
    "content": "<proposed-file-body>"
  }
}
```

### 4.2 Output

None on `additionalContext`. Block messages go to **stderr**:

```
Blocked: <new-slug> is similar to existing <existing-slug> (<score>%)
```

Claude Code forwards stderr to the user when the hook exits with code 2.

### 4.3 Contract

- Exit **2** if similarity ≥ 80 % on `root-cause` field (via `rapidfuzz.token_set_ratio`). Also writes `dedup-blocked|new>existing|session` to WAL.
- Exit **0** in all other cases (including: path not under `mistakes/`, file already exists, `uv` unavailable, `rapidfuzz` unavailable, empty content, empty root-cause, no similar file found). Optional `dedup-candidate` WAL entry for medium-similarity warnings.
- Hook MUST handle both pretool (file not yet on disk) and posttool (file exists) invocations — see `bin/memory-dedup.py` for the split.

### 4.4 Dependency degradation

If `uv` is not on `PATH`, the hook exits 0 silently. This is the documented optional-dependency path — see `docs/QUICKSTART.md` §1. Never silently-fail MUST here: the user must have made an explicit choice to skip dedup (by not installing `uv`).

---

## 5. PostToolUse hooks

hypomnema wires two PostToolUse hooks that do different things.

### 5.1 `memory-outcome.sh` — cascade and outcome signals

**Matcher:** `Write|Edit`
**Triggers on writes/edits to `~/.claude/memory/**/*.md`.**

Writes the following WAL events (as applicable):

- `outcome-new` — a new mistake file was just written.
- `cascade-review` — this file is listed as `related:` from another file; flag for review.
- `outcome-positive` / `outcome-negative` — deferred; actually emitted by Stop.

Exit 0 always. No stdout context.

### 5.2 `memory-error-detect.sh` — error pattern detection

**Matcher:** `Bash`

Reads `tool_response.stdout` and `tool_response.stderr`, matches against a curated list of error patterns (npm deprecations, permission denied, DeprecationWarning, etc.), and writes `error-detect|tool:category|session` to WAL. MAY emit a one-line hint to stdout for Claude's context.

Session-level deduplication: the same (tool, category) pair is written at most once per session.

Exit 0 always.

---

## 6. Stop

**Wire name:** `Stop`
**Timeout:** 10 s.
**Purpose:** Analyze the session that just ended, write outcome signals, generate continuity.

### 6.1 Input

```json
{
  "session_id": "<string>",
  "cwd": "<absolute-path>",
  "transcript_path": "<absolute-path>"
}
```

`transcript_path` points to a JSONL file with the session's message history. Stop hook MUST tolerate absence of this file (log, exit 0).

### 6.2 Output

```json
{
  "hookSpecificOutput": {
    "hookEventName": "Stop",
    "additionalContext": "[STRATEGIES] ...\n[MEMORY CHECKPOINT] ..."
  }
}
```

The output is a reminder string shown to Claude (not the user). It is advisory — it tells Claude what to save if anything surfaced this session.

### 6.3 Contract

- Exit 0 always.
- Writes at most one `clean-session` and one `session-metrics` event per session (by session_id).
- Generates / updates `continuity/<project>.md` from git state when the cwd is in a git repo and the project is known.
- MAY kick off background analytics rebuild when `.analytics-report` is stale (> 7 d). Background process MUST be detached and fail-safe.
- Reads trigger-match events to compute `trigger-useful` / `trigger-silent` for citation-detection.
- Rotation (`stale` → `archived`) runs here, on a per-type schedule (see FORMAT.md and CLAUDE.md for thresholds). Pinned files MUST NOT be rotated.

---

## 7. PreCompact

**Wire name:** `PreCompact`
**Timeout:** 5 s.
**Purpose:** Remind Claude to checkpoint insights to memory before context compression.

### 7.1 Input

```json
{
  "session_id": "<string>",
  "cwd": "<absolute-path>"
}
```

### 7.2 Output

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PreCompact",
    "additionalContext": "Before compaction: <reminder>"
  }
}
```

### 7.3 Contract

- Exit 0 always (cannot block compaction; observability only).
- Stronger reminder if zero memory files were written this session (`outcome-new` events count stays at 0).
- No WAL writes.

---

## 8. Running hooks manually

Each hook is a self-contained script that accepts its JSON envelope on stdin. This is how the test suite exercises them, and how you debug a failure:

```bash
# SessionStart
echo '{"session_id":"test","cwd":"/tmp"}' \
  | CLAUDE_MEMORY_DIR=/tmp/test-mem bash ~/.claude/hooks/memory-session-start.sh

# UserPromptSubmit
echo '{"session_id":"test","prompt":"help me with X"}' \
  | CLAUDE_MEMORY_DIR=/tmp/test-mem bash ~/.claude/hooks/memory-user-prompt-submit.sh

# PreToolUse (dedup)
echo '{"session_id":"t","tool_name":"Write","tool_input":{"file_path":"/tmp/m.md","content":"---\ntype: mistake\nroot-cause: \"X\"\n---"}}' \
  | CLAUDE_MEMORY_DIR=/tmp/test-mem bash ~/.claude/hooks/memory-dedup.sh
echo "exit=$?"  # 0 = allow, 2 = block
```

`HYPOMNEMA_TODAY` is your tool for freezing time in replay / diagnostics:

```bash
echo '{...}' | HYPOMNEMA_TODAY=2026-04-10 bash ~/.claude/hooks/memory-session-start.sh
```

---

## 9. Adding a new hook

Before emitting any new hook script or registering a new event type in `settings.json`:

1. Decide which of the existing hook types it fits into. Prefer extending an existing hook over adding a new one — `settings.json` patching is a visible user-facing change.
2. Document its wire shape in this file. Input, output, exit codes, WAL writes, timeout, failure mode.
3. Update the installer's directory-enumeration loop only if the new hook lives in a new directory (otherwise the enumeration picks it up automatically).
4. Add smoke-test coverage in `hooks/test-memory-hooks.sh`.

Do not ship a hook that has no documented contract here. If you need behaviour this document does not allow, propose a revision — don't invent silently.

---

## 10. Versioning

This document versions together with `FORMAT.md`. Any change to the wire shape, exit-code semantics, or fail-safe rule of an existing hook is a breaking change and requires a major version bump of both documents. Adding a new hook type or a new optional environment variable is non-breaking.
