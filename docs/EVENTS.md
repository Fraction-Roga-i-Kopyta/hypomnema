# WAL events — normative registry

Source of truth for hypomnema WAL event types. Adding, deprecating, or
changing the `target` shape of an event is a contract change and MUST
be recorded here in the same commit that ships the change. The
extension process is in [`FORMAT.md § 5.3`](FORMAT.md#53-extension-rules);
this file is the durable index that other docs (CHANGELOG, hook
comments, test fixtures) reference by event name.

## Schema reminder

Every WAL line carries exactly four pipe-delimited columns:

```
date|event|target|session
```

This registry catalogs `event` values and per-event semantics for
`target`. Pipes and newlines in source data MUST be replaced with `_`
before write — see [`FORMAT.md § 5`](FORMAT.md#5-wal-grammar) and the
`pathutil.SlugFromPath` sanitiser added in v1.0.1.

## Status legend

- **Active** — emitted by current code; readers MUST handle.
- **Deprecated** — emitted by past versions only; readers MUST
  tolerate (skip without crash), MUST NOT emit.
- **Reserved** — defined but not yet emitted; activation criteria in
  the linked ADR or follow-up issue.

## Closing legend

`closed?` column marks events that count toward soft-close in
`hooks/wal-retro-silent.sh` (see [`FORMAT.md § 5.2`](FORMAT.md#52-closing-events--soft-close-semantics-v014)):

- ✓ — closes a session (presence prevents retroactive silent classification)
- — — does not close a session

---

## Registry

### Core injection lifecycle

| event | target shape | producer | consumer | first-seen | status | closed? |
|---|---|---|---|---|---|:-:|
| `inject` | `<slug>` | `hooks/session-start.sh` | `hooks/session-stop.sh` (feedback loop), `bin/memory-self-profile.sh` | v0.1.0 | Active | — |
| `inject-agg` | `<slug>,<count>` (comma sub-delim) | `hooks/wal-compact.sh` | `bin/memory-self-profile.sh` | v0.1.0 | Active | — |

### Retrieval signals

| event | target shape | producer | consumer | first-seen | status | closed? |
|---|---|---|---|---|---|:-:|
| `trigger-match` | `<slug>` | `hooks/user-prompt-submit.sh` | `bin/memory-strategy-score.sh`, `bin/memory-self-profile.sh` | v0.5.0 | Active | — |
| `trigger-useful` | `<slug>` | `hooks/session-stop.sh` (feedback-loop) | `bin/memory-self-profile.sh`, `internal/profile`, `memoryctl decisions review` | v0.7.1 | Active | ✓ |
| `trigger-silent` | `<slug>` | `hooks/session-stop.sh` (feedback-loop) | `bin/memory-self-profile.sh`, `internal/profile`, `memoryctl decisions review` | v0.7.1 | Active | ✓ |
| `trigger-silent-retro` | `<slug>` | `hooks/wal-retro-silent.sh` | `bin/memory-self-profile.sh`, `internal/profile` | v0.13.0 | Active | ✓ |
| `shadow-miss` | `<slug>` | `hooks/memory-fts-shadow.sh`, `internal/fts` | `hooks/memory-analytics.sh` (Recall gaps), `memoryctl decisions review` (`shadow_miss_ratio`) | v0.8.2 | Active | — |

### Outcomes

| event | target shape | producer | consumer | first-seen | status | closed? |
|---|---|---|---|---|---|:-:|
| `outcome-new` | `<slug>` | `hooks/memory-outcome.sh` (PostToolUse:Write/Edit on `mistakes/`) | `bin/memory-self-profile.sh` | v0.2.0 | Active | ✓ |
| `outcome-positive` | `<slug>` | `hooks/memory-outcome.sh` (feedback-loop verdict) | `bin/memory-self-profile.sh`, `internal/profile`, ADR `cold-start-scoring` | v0.2.0 | Active | ✓ |
| `outcome-negative` | `<slug>` | `hooks/memory-outcome.sh` (feedback-loop verdict) | `bin/memory-self-profile.sh`, `internal/profile` | v0.2.0 | Active | ✓ |
| `clean-session` | `<domain>` or `_global_` | `hooks/session-stop.sh` (zero errors only) | `bin/memory-self-profile.sh`, `internal/profile`, `bin/memory-strategy-score.sh` | v0.2.0 | Active | ✓ |

### Session rollup

| event | target shape | producer | consumer | first-seen | status | closed? |
|---|---|---|---|---|---|:-:|
| `session-metrics` | v2: `domains:<csv>,error_count:N,tool_calls:M,duration:Ss` (`$3`); session id (`$4`). v1 (legacy): `<domains_csv>` (`$3`); `<key:val,...>` blob (`$4`) | `hooks/session-stop.sh` | `internal/profile/splitSessionMetrics`, `bin/memory-self-profile.sh`, `hooks/memory-analytics.sh`, `hooks/wal-compact.sh` | v0.2.0 (v1) / v1.0.0 (v2) | Active | — (use `session-close`) |
| `session-close` | `<session_id>` (`$4` slot) | `hooks/session-stop.sh` (any error count) | `hooks/wal-retro-silent.sh` (closing check), `internal/profile` | v0.14.0 | Active | ✓ |

### Strategy signals

| event | target shape | producer | consumer | first-seen | status | closed? |
|---|---|---|---|---|---|:-:|
| `strategy-used` | `<slug>` | `hooks/memory-outcome.sh` | `bin/memory-strategy-score.sh`, `bin/memory-self-profile.sh` | v0.2.0 | Active | — |
| `strategy-gap` | `<domain>` | `hooks/session-stop.sh` (clean session w/o strategy hit) | `bin/memory-self-profile.sh` | v0.2.0 | Active | — |
| `strategy-rescored` | `<slug>` | `bin/memory-strategy-score.sh` | (informational only) | v0.7.0 | Active | — |

### Dedup

| event | target shape | producer | consumer | first-seen | status | closed? |
|---|---|---|---|---|---|:-:|
| `dedup-blocked` | `<new-slug>><existing-slug>` (`>` sub-delim) | `hooks/memory-dedup.sh` (PreToolUse), `internal/dedup` | (informational) | v0.10.0 | Active | — |
| `dedup-merged` | `<new-slug>><existing-slug>` | `hooks/memory-dedup.sh` (PostToolUse), `internal/dedup` | (informational) | v0.10.0 | Active | — |
| `dedup-candidate` | `<new-slug>~<existing-slug>` (`~` sub-delim) | `hooks/memory-dedup.sh`, `internal/dedup` | (informational) | v0.10.0 | Active | — |

### Cluster / cascade

| event | target shape | producer | consumer | first-seen | status | closed? |
|---|---|---|---|---|---|:-:|
| `cluster-load` | `<src-slug>><target-slug>` | `hooks/session-start.sh` (cluster expansion) | `bin/memory-self-profile.sh` | v0.4.0 | Active | — |
| `cascade-review` | `<parent-slug>:<child-slug>` (`:` sub-delim) | `hooks/memory-outcome.sh` (cascade marker apply) | (informational) | v0.4.0 | Active | — |

### Lifecycle / rotation

| event | target shape | producer | consumer | first-seen | status | closed? |
|---|---|---|---|---|---|:-:|
| `rotation-stale` | `<slug>` | `hooks/session-stop.sh` (rotation pass) | (informational) | v0.7.0 | Active | — |
| `rotation-archive` | `<slug>` | `hooks/session-stop.sh` (rotation pass) | (informational) | v0.7.0 | Active | — |
| `rotation-summary` | `stale:N,archive:M` (comma sub-delim) | `hooks/session-stop.sh` (per pass) | (informational) | v0.7.0 | Active | — |

### Schema / health

| event | target shape | producer | consumer | first-seen | status | closed? |
|---|---|---|---|---|---|:-:|
| `schema-error` | `<slug>` | `hooks/lib/parse-memory.sh`, multiple hooks | `hooks/memory-analytics.sh` | v0.6.0 | Active | — |
| `format-unsupported` | `<slug>:N` (`N` is the unsupported `format_version`) | `hooks/lib/parse-memory.sh` | `hooks/memory-analytics.sh` | v1.0.0 | Active | — |
| `evidence-missing` | `<phrase-hash>` | `hooks/lib/feedback-loop.sh` | `hooks/memory-analytics.sh` | v0.7.1 | Active | — |
| `evidence-empty` | `<slug>` | `hooks/lib/feedback-loop.sh` | `bin/memory-self-profile.sh` | v0.7.1 | Active | — |
| `error-detect` | `<tool>:<category>` (`:` sub-delim) | `hooks/memory-error-detect.sh` (PostToolUse) | (informational) | v0.6.0 | Active | — |
| `index-stale` | `<index-name>` (e.g. `tfidf`, `fts`) | `hooks/memory-index.sh`, `bin/memory-fts-sync.sh` | (informational) | v0.10.0 | Active | — |

### Reserved (not yet emitted)

| event | planned producer | activation criteria |
|---|---|---|
| `cascade-predictive` | predictive cascade extension (v0.9 release candidate) | observed recall-gap pattern of co-misses; ADR `cascade-predictive.md` to be written at activation. See `notes/memory-system-roadmap.md § v0.9 «Предвидение»` (in maintainer's local memory). |

---

## Authoring rules

When adding a new event:

1. **Reserve a row in this file first** with status `Reserved` and the
   activation criteria. The PR that activates the event flips status
   to `Active` and fills in producer/consumer/first-seen.
2. **Pick a name** that follows `<domain>-<action>` (`cluster-load`,
   not `load_cluster`). Lowercase kebab-case only. Avoid suffixes
   that look like a hierarchy unless they really are one
   (`outcome-new` / `outcome-positive` / `outcome-negative` are
   genuine siblings; `error-detect` is single).
3. **Encode rich data into `target`** with sub-delimiters. The four-
   column invariant is immutable within format v1; a fifth column
   requires a major version (see FORMAT.md § 5.3). Established sub-
   delimiters are `,` (rows of equal weight, `inject-agg`,
   `rotation-summary`), `:` (key:value, `error-detect`,
   `cascade-review`, `format-unsupported`), `>` (directed pair,
   `dedup-*`, `cluster-load`), `~` (similarity / soft pair,
   `dedup-candidate`).
4. **Decide closing membership** if the event signals session
   completion. Add it to the soft-close set in [`FORMAT.md § 5.2`](FORMAT.md#52-closing-events--soft-close-semantics-v014)
   AND the `closed[]` array in `hooks/wal-retro-silent.sh`. Set
   `closed?` to ✓ in the registry row above.
5. **Surface to readers if the event is observational**:
   `bin/memory-self-profile.sh` and/or `internal/profile` should
   count it; `hooks/memory-analytics.sh` should report it if the
   user can act on it.
6. **Add a `memoryctl wal validate` rule** if the `target` shape is
   structurally constrained (e.g. fixed sub-delimiters, slug-only).
   The validator (planned v1.1) reads this registry to decide what
   to enforce.
7. **Update `CHANGELOG.md`** with an entry under the relevant version,
   citing the row added here. Cross-reference is one-way: this file
   is the authoritative spec; CHANGELOG narrates the addition.

When deprecating an event:

1. Flip status to `Deprecated` and add a row note explaining the
   replacement event (or "no replacement; readers should skip").
2. Stop emitting in code but continue tolerating in readers
   indefinitely — historical WALs may carry the event for years.
3. Do NOT delete the registry row. Deprecated events stay listed so
   readers know the row was deliberate, not an oversight.

---

## Glossary of `target` sub-delimiters

| sub-delim | meaning | example events |
|---|---|---|
| `,` | rows of equal weight (CSV-like) | `inject-agg`, `rotation-summary`, `session-metrics` (v2 inner) |
| `:` | `key:value` (single pair, or namespace prefix) | `error-detect`, `cascade-review`, `format-unsupported`, `session-metrics` (v2 inner) |
| `>` | directed pair (`from > to`, asymmetric) | `dedup-blocked`, `dedup-merged`, `cluster-load` |
| `~` | soft / similarity pair (symmetric) | `dedup-candidate` |

A new sub-delimiter requires an entry here AND a worked example in
the registry above. Adding sub-delimiters is non-breaking
(readers ignore unknown structure), but introducing them haphazardly
makes the WAL harder to grep and harder to validate.
