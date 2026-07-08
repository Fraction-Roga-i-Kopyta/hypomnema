# Contributing

Thanks for looking. Hypomnema is a solo project (single maintainer)
and this document exists so contribution attempts don't end in wasted
work for either side.

## Current posture

- **Issues: open.** Bug reports, reproduction instructions, design
  questions — file them. Please include `memoryctl doctor` output
  and the relevant lines from `~/.claude/memory/.wal`.
- **Pull requests: by invitation.** The project is on the v2.x line
  with a contract (`docs/FORMAT.md`, `docs/hooks-contract.md`,
  `docs/INVARIANTS.md`) that's marked STABLE but hasn't survived
  broad external usage yet. A PR for anything that touches
  `internal/`, `cmd/memoryctl/`, the hook shims, or the format
  contract is likely to collide with in-flight work. Open an issue
  first, get a go-ahead, then PR.
- **Docs / comment fixes: welcome** as PRs without prior discussion.
  Typos, clearer wording, broken links — just send them.

Track direction in [`CHANGELOG.md`](CHANGELOG.md) and the ADRs under
[`docs/decisions/`](docs/decisions/README.md).

## Before opening a PR

1. **Read [`docs/decisions/`](docs/decisions/README.md).** The 15
   ADRs there explain load-bearing choices (Go-primary with bash
   shims, silent-fail policy, the single v2 ranker, format
   invariants). A PR that would invalidate an ADR needs to call out
   which one and why — often the answer is to open a discussion, not
   to ship the change.
2. **Build and run the suite locally** (Go 1.25):
   ```bash
   make build     # compile bin/memoryctl (CGO_ENABLED=0, static)
   make test      # go test -race ./...
   make lint      # staticcheck v0.7.0 across the repo
   ```
   `make test` is `go test -race ./...` over all 18 packages under
   `internal/` plus `cmd/memoryctl`. There is no separate bash test
   or parity target — v2 build/test orchestration is Go-only; the
   bash side is a thin shim layer covered by the shim contract test
   below.
3. **Match what CI enforces.** `.github/workflows/test.yml` runs three
   jobs:
   - **test** (matrix: `ubuntu-latest` + `macos-latest`) —
     `make build`, `go vet ./...`, `go test -race ./...`, the
     invariants audit (`go run ./cmd/memoryctl audit invariants`),
     the shim contract tests (`bash hooks/v2/shims_test.sh
     ./bin/memoryctl`), the install/uninstall sandbox
     (`bash scripts/install_test.sh`), and the WAL grammar gate on a
     fixture (`CLAUDE_MEMORY_DIR=fixtures/wal-clean ./bin/memoryctl
     wal validate`).
   - **lint** (`ubuntu-latest`) — `make lint` (staticcheck).
   - **shellcheck** (`ubuntu-latest`) — `shellcheck -e SC1091
     -S error` across every `*.sh` outside `.git/` and `docs/`.

   Running `make build && make test && make lint` plus
   `go run ./cmd/memoryctl audit invariants` locally reproduces the
   Go gates; the shim/install/WAL steps are quick to run by hand from
   the commands above.
4. **Verify the diff doesn't cross the contract invariants** in
   [`docs/INVARIANTS.md`](docs/INVARIANTS.md). Load-bearing ones:
   - WAL grammar stays four pipe-delimited columns (I1); slugs in the
     `target` column have `|`/`\n`/`\r` sanitised (I2).
   - Multi-line files in a memory store are written atomically (I3),
     and exported functions taking a path argument keep their
     boundary check (I4).
   - Hooks exit 0 on any internal failure, except the sanctioned
     `PreToolUse` exit 2 for dedup/secrets blocks (I5).
   - `status: pinned` protects a file from rotation but does not
     reserve an injection slot (I6).
   - Behaviour still duplicated across the bash shims and Go stays in
     lock-step (I7). If a planned divergence is correct, document it
     in the relevant ADR.

## Commit style

- One logical change per commit.
- Commit messages describe **what changed and why**, not who surfaced
  it. Don't cite "external review" or "audit X" — that context
  belongs in the PR description.
- Reference the commit title style you see in `git log` — imperative
  verb, scope in parens where useful: `fix(guard): …`,
  `feat(inject): …`, `docs(changelog): …`.

## Reporting a security concern

See [`SECURITY.md`](SECURITY.md).

## Not looking for

- Vector embeddings, cloud memory, LLM-in-the-retrieval-loop — these
  are explicit non-goals of the 2.x line.
- Team-shared memory. Hypomnema's design assumes `~/.claude/memory/`
  is user-owned; multi-user sharing is a fork, not a merge.
- Feature parity with vendor-managed memory tools (ChatGPT memory,
  Cursor memory). Hypomnema is deliberately narrower: local files,
  no opaque storage, diffable state.
