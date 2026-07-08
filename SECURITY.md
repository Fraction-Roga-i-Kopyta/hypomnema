# Security

## Reporting a vulnerability

Prefer GitHub's **private vulnerability reporting** — submit a report at
<https://github.com/Fraction-Roga-i-Kopyta/hypomnema/security/advisories/new>.
No public issue, no disclosure until a fix lands.

Include:

- Steps to reproduce.
- Affected version / commit hash (`git rev-parse HEAD` is enough).
- Impact: what an attacker could do, under what preconditions.
- Suggested fix, if obvious.

Maintainer responds within a week. If the issue is confirmed, expect
a fix tagged as a PATCH release with the disclosure noted in
`CHANGELOG.md` under `### Security`. Coordinated disclosure is
welcome; timeline discussed in the advisory thread.

Do **not** open a public GitHub issue for a vulnerability until a fix
is on `main`.

## Threat model

Hypomnema runs entirely on the user's machine. `memoryctl` and its
hook shims make **no outbound network calls** — production Go imports
neither `net` nor `net/http` (the only `http(s)://` strings in the
tree are a comment in `internal/secrets/secrets.go` documenting the
credential-URL regex plus connection-string fixtures under
`internal/secrets/*_test.go`, none of which open a socket).
Consequently:

- **Out of scope:** remote code execution from untrusted network
  input. The project consumes no network input.
- **Out of scope:** multi-tenant isolation. Hypomnema assumes a
  single user owns `~/.claude/memory/`; `umask 077` is the default
  on runtime artefacts, but cross-user hardening past that is not a
  goal.
- **In scope:** anything that lets an LLM-authored memory file
  escalate beyond `markdown-inert content`:
  - **Write-scope confusion / path traversal.** The secret gate must
    police *exactly* the memory stores and nothing else. `guardedRel`
    (`cmd/memoryctl/guard.go`) maps a write's `file_path` into the
    store that owns it — the legacy runtime tree
    (`$CLAUDE_MEMORY_DIR`), any native per-project store
    (`~/.claude/projects/<slug>/memory/`), or the global store
    (`~/.claude/memory-global/`) — and returns "not guarded" for
    anything outside all three. Session-id components that reach the
    filesystem are collapsed to a single safe path segment by
    `internal/pathutil.SafeFileName` (letters, digits, `.`, `_`, `-`
    survive; separators and `..` become `_`), and multi-line memory
    artefacts are written through `pathutil.WriteFileAtomic`.
  - **Accidental plaintext-secret exposure from memory writes**
    (below).
- **In scope:** accidental plaintext-secret exposure from memory
  writes. `memoryctl guard` runs as a `PreToolUse` hook (matcher
  `Write|Edit`, also covering `NotebookEdit` and `MultiEdit`
  payloads) and blocks a write whose content carries a credential
  outside fenced code, using the `internal/secrets` detector:
  - **Key-based matches.** A key whose token *contains* `api_key` /
    `apikey` / `secret` / `password` / `passphrase` / `token`
    (so `db_password`, `client_secret`, `AWS_SECRET_ACCESS_KEY` and
    other snake_case / SCREAMING_SNAKE forms all match, not just a
    bare camelCase word), followed by `:` or `=` and a value of ≥ 8
    non-space characters, quoted or unquoted. The substring match
    was widened in v2.5.1 — a `\b`-anchored keyword used to let the
    canonical config/env spelling through.
  - **Value-based matches** (the value alone is sufficient, no key
    needed): AWS access/STS key ids (`AKIA…` / `ASIA…`), GitHub
    tokens (`ghp_…`, `gho_…`, `ghu_…`, `ghs_…`, `ghr_…` and
    fine-grained `github_pat_…`), Anthropic keys (`sk-ant-…`), Slack
    tokens (`xox[bpars]-…`), PEM `BEGIN … PRIVATE KEY` blocks, JWTs
    (`eyJ….eyJ….sig`), and `scheme://user:pass@` URL credentials.
  - **Code-fence handling.** Fenced and inline code is exempt, but an
    *unclosed* fence does not exempt the remainder of the file (the
    orphan opener and everything after it are scanned), and a fence
    delimiter's own info-string is still scanned — a secret parked as
    ```` ```<secret> ```` renders as an empty code block yet sits in
    plaintext in the raw file.
  - **Fail-open.** Bad JSON, an empty/whitespace payload, a path
    outside every store, or a whitelisted path all exit 0 (allow).
    The only non-zero exit is a definite hit: exit 2 with a stderr
    message naming the file and the `line: fragment` of each match.

  Escape hatches:
  - **Permanent whitelist:** add a glob to
    `~/.claude/memory-global/.secretsignore` (the documented
    user-facing location; the legacy runtime tree's
    `.secretsignore.default` and `.secretsignore` are also consulted,
    last-match-wins, `!` negates). Over-broad patterns (`*`, `**`,
    `**/*`, or any run of only `*`/`/`) are **ignored** — otherwise a
    single non-secret write to `.secretsignore` could silently
    disable the gate for the whole store.
  - **One-shot override:** set `HYPOMNEMA_ALLOW_SECRETS=1` for a
    single invocation and retry.

## Known behaviours that look like vulnerabilities but aren't

- **Hooks exit 0 on internal failure.** This is the documented
  fail-safe contract (`docs/hooks-contract.md`, enforced as invariant
  I5 in `docs/INVARIANTS.md`). A broken memory file or unreadable
  input does not break the user's Claude Code session; observability
  moves to WAL events + `memoryctl doctor`. The one sanctioned
  non-zero exit is `PreToolUse` exit 2 for a dedup or secrets block.
  If you think a hook should exit non-zero, that's a design
  conversation for an issue, not a security report.
- **Memory files are LLM-authored.** Every file under a memory store
  was written either by the user or by Claude through the Write tool.
  Their contents are user-trusted by design; the secrets gate is
  defence-in-depth, not authorization.
- **WAL contains session_ids.** Claude Code UUIDs, not PII. If you
  share a memory store publicly (dotfiles repo etc.), the runtime
  `.runtime/` and `.wal` are typically gitignored — verify before
  pushing.

## What I won't treat as a vulnerability

- Reports that `self-profile.md` can be manipulated by editing
  `.wal`. The user owns the WAL; manipulation requires local
  filesystem access already.
- Reports that a memory file can change scoring/ranking behaviour.
  That's the intended purpose — good `keywords`/`domains` are the
  ranking signal, and the ranker is additive so no single field can
  do more than move a fact up or down the list.
- Reports that a malformed JSON envelope from Claude Code can
  crash a hook. Hooks exit 0 on bad input (`memoryctl guard` returns
  0 the moment `json.Unmarshal` fails); the user's session continues.
