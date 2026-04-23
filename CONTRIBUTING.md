# Contributing

Thanks for looking. Hypomnema is a solo project (single maintainer,
13 commits/day at peak) and this document exists so contribution
attempts don't end in wasted work for either side.

## Current posture

- **Issues: open.** Bug reports, reproduction instructions, design
  questions — file them. Please include `memoryctl doctor` output
  and the relevant lines from `~/.claude/memory/.wal`.
- **Pull requests: by invitation.** The project is pre-1.0 with a
  contract (`docs/FORMAT.md`, `docs/hooks-contract.md`) that's marked
  STABLE but hasn't survived external usage yet. A PR for anything
  that touches `internal/`, the hook layer, or the format contract
  is likely to collide with in-flight work. Open an issue first,
  get a go-ahead, then PR.
- **Docs / comment fixes: welcome** as PRs without prior discussion.
  Typos, clearer wording, broken links — just send them.

After the first external review has landed and a v1.0 format has been
frozen, this posture will loosen. Track progress in
[`docs/plans/`](docs/plans/) and
[`CHANGELOG.md`](CHANGELOG.md).

## Before opening a PR

1. **Read [`docs/decisions/`](docs/decisions/README.md).** The 8 ADRs
   there explain load-bearing choices (bash+Go hybrid, silent-fail,
   two scoring pipelines, format invariants). A PR that would
   invalidate an ADR needs to call out which one and why — often the
   answer is to open a discussion, not to ship the change.
2. **Run the suite locally:**
   ```bash
   make build
   make test      # 272+ bash assertions
   make parity    # 6+ bash↔Go parity cases
   go test ./...  # Go unit tests across 11 internal packages
   ```
3. **Verify the diff doesn't cross these invariants** (from
   `docs/ARCHITECTURE.md`):
   - WAL grammar stays four pipe-delimited columns.
   - `self-profile.md`, `_agent_context.md`, `MEMORY.md` stay out of
     the FTS index (strange-loop hazard).
   - Hooks exit 0 on any internal failure (except PreToolUse exit 2
     for dedup/secrets blocks).
   - Bash and Go implementations of the same component stay in
     lock-step via `make parity`. If a planned divergence is
     correct, document it in the relevant ADR.

## Commit style

- One logical change per commit.
- Commit messages describe **what changed and why**, not who surfaced
  it. Don't cite "external review" or "audit X" — that context
  belongs in the PR description.
- Reference the commit title style you see in `git log` — imperative
  verb, scope in parens where useful: `fix(hooks): …`,
  `feat(decisions): …`, `docs(changelog): …`.

## Reporting a security concern

See [`SECURITY.md`](SECURITY.md).

## Not looking for

- Vector embeddings, cloud memory, LLM-in-the-retrieval-loop — these
  are [explicit non-goals](docs/plans/2026-04-23-roadmap-v0.10.5-onward.md#items-explicitly-not-in-this-roadmap)
  of the 1.x line.
- Team-shared memory. Hypomnema's design assumes `~/.claude/memory/`
  is user-owned; multi-user sharing is a fork, not a merge.
- Feature parity with vendor-managed memory tools (ChatGPT memory,
  Cursor memory). Hypomnema is deliberately narrower: local files,
  no opaque storage, diffable state.
