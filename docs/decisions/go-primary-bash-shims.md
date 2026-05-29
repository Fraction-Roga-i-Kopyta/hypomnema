---
type: decision
project: global
created: 2026-05-29
status: active
description: Go (memoryctl) is the primary implementation; bash is reduced to ~10-line exec shims. Flips bash-go-gradual-port.md, which made bash the reference.
keywords: [go, bash, shims, architecture, v2, parity]
domains: [architecture, build]
related: [native-primary-sidecar, file-based-memory]
---

## What

**This ADR supersedes `bash-go-gradual-port.md` (v2.0).**

In v1.x, bash was the reference implementation and Go was an opt-in
accelerator for hot paths, gated by a byte-for-byte parity check in CI.
Pure-bash installs worked end-to-end; `make build` was optional.

v2.0 flips that model: **Go is the primary implementation**; bash is
reduced to four thin shims of ~10 lines each. Each shim marshals
env/stdin into command arguments and `exec`s the binary — zero logic
lives in shell. The parity-gate ADR is retired because there is no
longer a bash reference path to compare against.

Four shims, one responsibility each:

| Shim | Hook event | What it does |
|---|---|---|
| `session-start.sh` | SessionStart | marshall env → `memoryctl inject --event=session-start` |
| `user-prompt-submit.sh` | UserPromptSubmit | marshall env + stdin prompt → `memoryctl inject --event=prompt` |
| `pre-tool-write.sh` | PreToolUse:Write | marshall file path from stdin → `memoryctl guard` |
| `session-stop.sh` | Stop | marshall env → `memoryctl close` |

The Go binary (`memoryctl`) implements:

- `inject` — ranked top-K injection (unified pipeline, see
  `two-scoring-pipelines.md` which this supersedes).
- `guard` — secrets-gate + dedup check.
- `close` — outcome attribution, WAL append, decay recompute,
  self-profile regeneration, continuity write.
- `migrate` — one-shot v1→v2 conversion + pruning.
- `doctor` / `profile` — health and precision reporting.
- `ab` — A/B null-hypothesis harness.

The `internal/native` package is the sole adapter for native format.
`internal/rank` is a pure function with no I/O — unit-testable and
A/B-replayable without a running harness.

## Why

Three v1 pain points that accumulate into an architectural decision:

1. **bash 3.2 maintenance tax (macOS).** Every awk/sed/stat call
   needs a BSD vs GNU variant. Associative arrays, `mapfile`,
   `${var,,}` are off-limits. Float arithmetic requires `bc` or `awk`.
   This is not a portability edge-case — it is the primary development
   machine. The v1 CI matrix grew explicit platform probes to catch
   divergences; v2 eliminates the surface.

2. **JSON-safety at the hook boundary.** Hook scripts receive
   JSON-encoded input (prompt, file paths, env). Extracting values via
   `jq` → shell variables → re-encoding is the v1 pattern. Any value
   with `"`, `\`, or a newline breaks the pipeline unless every step
   adds escaping. Go's `encoding/json` encodes correctly by
   construction; this property eliminates the v1 `perl` tag-injection
   guard entirely.

3. **Performance on the hot path.** SessionStart awk + subshell
   explosion on large corpora (200+ files, 50k+ WAL lines) was the
   main motivation for the gradual port in the first place. v2's
   `inject` runs in <1s warm on a 200-file corpus with a soft
   deadline (`rank`'s 800ms budget returns best-so-far on timeout,
   exit 0). The hot path is now 100% Go — no awk, no subshells, no
   SQLite CLI calls.

The parity-gate maintenance cost from `bash-go-gradual-port.md` is
also resolved by this flip: there is one implementation, not two.

**`go-parity-gate-for-shell-portability`** strategy (in the memory
store) is retired along with `bash-go-gradual-port.md` — it described
keeping a bash reference for the parity check, which no longer applies.

## Trade-off accepted

- **Go is a build prerequisite.** `make build` is no longer optional;
  `./install.sh` refuses on Claude Code < 2.1.59 or without a usable
  `go` binary. This drops pure-shell install as a supported path. The
  target audience (users with Claude Code v2.1.59+) already runs a
  modern toolchain; the tradeoff is accepted. The install gate prints
  an actionable message: upgrade Claude Code, or stay on the v1.x tag.
- **Four shims, not zero.** The Claude Code hook boundary is JSON-in
  /stdout-out; `memoryctl` is a CLI. The shims are the translation
  layer. They are intentionally ~10 lines with no logic — any logic
  that creeps in is a bug to fix in the Go binary instead.
- **Bash-only fallback is gone.** Users who previously relied on the
  bash path for environments where building Go is impractical have no
  fallback. v1.x remains on its tag, fully functional, for those cases.
  No back-ports of v2 features to v1.x are planned.

## When to revisit

If Claude Code exposes a native plugin API (compiled extension, not a
hook shim), the bash-shim layer may become unnecessary. Evaluate at
that point whether the shims can be replaced by a direct hook
registration from the Go binary.

If the build prerequisite becomes a documented barrier (e.g. issues
from users unable to install Go), consider a pre-compiled binary
distribution via the GitHub release artifact instead of source build.
