---
type: decision
project: global
created: 2026-04-22
status: active
description: Bash is the reference implementation; Go hot paths are opt-in and gated by a byte-for-byte parity check in CI.
keywords: [bash, go, parity, hybrid]
domains: [architecture, build]
related: [file-based-memory]
review-triggers:
  - after: "2027-04-22"
---

## What

Hook scripts, context assembly, WAL append, and orchestration remain
in bash. A `memoryctl` Go binary implements the hot paths where bash
+ awk + sqlite3-CLI are materially slow: FTS5 shadow recall, FTS5
sync, fuzzy dedup, single-pass WAL aggregation for self-profile,
unicode-aware TF-IDF index rebuild. Each bash script that has a Go
equivalent tries `$PATH` → `~/.claude/bin/memoryctl` first, then
`exec memoryctl <subcommand>`; otherwise it falls through to the awk
path. `scripts/parity-check.sh` runs on every push to enforce
byte-for-byte equality on a shared fixture.

Pure-bash installs (`git clone` + `./install.sh`, no `make build`)
work end-to-end with the bash-only paths — fuzzy dedup is quietly
off, TF-IDF tokeniser is Latin-only. Go-enabled installs gain ~5×
speed on WAL-aggregation and correctness on non-Latin corpora.

## Why

Three pressures, none of which have a one-language answer:

1. **Cold-start cost of a Go binary.** First-time users should be
   able to clone, install, and get value in under a minute. A
   `make build` gate would push that to 5+ minutes and exclude
   users without Go installed. Bash-first keeps the door open.
2. **bash latency on bigger corpora.** Awk + subshell explosion
   scales badly past 500 memory files and 10 000 WAL lines.
   Self-profile generation alone is 10× slower in bash at 50k WAL
   lines. Users who hit that wall benefit from the Go escape.
3. **Portability of shell as the universal glue.** Hooks have to
   parse JSON envelopes, mutate `settings.json`, and interoperate
   with the user's `~/.claude/bin`. Bash + jq is the set of tools
   every developer machine already has; Go isn't.

## Trade-off accepted

- Every feature in a hot-path component ships in two
  implementations that must stay congruent. The TF-IDF tokeniser
  already diverges intentionally: bash is Latin-only (awk byte
  class), Go is unicode-aware. The parity case for TF-IDF is
  restricted to a Latin-only fixture and the divergence is
  documented as an intentional limitation of the fallback path.
- `scripts/parity-check.sh` is an actively-paid fitness function.
  Every new subcommand needs a fixture case before merge. The
  alternative — let the two drift — breaks the "fallback is
  adequate" contract.
- Bash can't be deleted later without a breaking install story.
  That locks the project into a hybrid for as long as pure-shell
  installs are a target audience.

## When to revisit

If the pure-bash install story becomes untenable — say, the hook
contract grows a feature that can't be expressed in shell without
contortions — or if a second Go parity divergence appears that
can't be scoped to a documented fixture subset. Either is evidence
the fallback is bleeding into the primary path.
