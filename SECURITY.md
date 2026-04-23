# Security

## Reporting a vulnerability

Email **oleksiy.ohmush@boosta.co** with:

- Steps to reproduce.
- Affected version / commit hash (`git rev-parse HEAD` is enough).
- Impact: what an attacker could do, under what preconditions.
- Suggested fix, if obvious.

I'll reply within a week. If the issue is confirmed, expect a fix
tagged as a PATCH release with the disclosure noted in `CHANGELOG.md`
under `### Security`. Coordinated disclosure is welcome; timeline
discussed in the email thread.

Do **not** open a public GitHub issue for a vulnerability until a fix
is on `main`.

## Threat model

Hypomnema runs entirely on the user's machine. There is no network
call in the production code path — `grep -rE "https?://" hooks/ bin/
internal/ cmd/` returns zero matches as an enforced invariant
(`docs/ARCHITECTURE.md § Invariants`). Consequently:

- **Out of scope:** remote code execution from untrusted network
  input. The project consumes no network input.
- **Out of scope:** multi-tenant isolation. Hypomnema assumes a
  single user owns `~/.claude/memory/`; `umask 077` is the default
  on runtime artefacts, but cross-user hardening past that is not a
  goal.
- **In scope:** anything that lets an LLM-authored memory file
  escalate beyond `markdown-inert content`:
  - Shell/command injection via hook-sourced config or frontmatter
    values. `.config.sh` goes through a safe parser
    (`hooks/lib/load-config.sh`), not `source`, precisely because
    Claude can write it.
  - Path traversal through `related:` slugs
    (`hooks/lib/parse-memory.sh` guard at CWE-22).
  - Structural prompt-marker injection through `additionalContext`
    (memory bodies have `<system-reminder>`, `<tool_use>`, etc.
    entity-encoded before JSON serialisation).
  - Regex metacharacter injection through memory filenames
    (`_perl_re_escape` helper).
- **In scope:** accidental plaintext-secret exposure from memory
  writes. `hooks/memory-secrets-detect.sh` (v0.13+) runs as a
  PreToolUse hook and blocks writes containing api_key / password /
  token patterns ≥ 8 chars outside fenced code blocks. Bypass for
  legitimate documentation: `HYPOMNEMA_ALLOW_SECRETS=1`.

## Known behaviours that look like vulnerabilities but aren't

- **Hooks exit 0 on internal failure.** This is the documented
  fail-safe contract (`docs/hooks-contract.md §1.3`). A broken memory
  file or unreadable config does not break the user's Claude Code
  session; observability moves to WAL events + `memoryctl doctor`.
  If you think a hook should exit non-zero, that's a design
  conversation for an issue, not a security report.
- **Memory files are LLM-authored.** Every file under
  `~/.claude/memory/` was written either by the user or by Claude
  through the Write tool. Their contents are user-trusted by design;
  secrets-detect is defence-in-depth, not authorization.
- **WAL contains session_ids.** Claude Code UUIDs, not PII. If you
  share `~/.claude/memory/` publicly (dotfiles repo etc.),
  `.runtime/` and `.wal` are typically gitignored — verify before
  pushing.

## What I won't treat as a vulnerability

- Reports that `self-profile.md` can be manipulated by editing
  `.wal`. The user owns the WAL; manipulation requires local
  filesystem access already.
- Reports that `.config.sh` can change scoring behaviour. That's
  the intended purpose — `hooks/lib/load-config.sh` is a whitelist
  parser precisely so LLM-authored values can't do more than that.
- Reports that a malformed JSON envelope from Claude Code can
  crash a hook. Hooks exit 0 on bad input; the user's session
  continues.
