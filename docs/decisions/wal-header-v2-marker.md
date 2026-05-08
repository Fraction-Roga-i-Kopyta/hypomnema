---
type: decision
project: global
created: 2026-05-08
status: proposed
description: An optional first-line magic marker `# hypomnema-wal v2` lets readers fail loudly on truly malformed WAL instead of silently parsing it as v1; readers MUST tolerate the marker's absence (legacy WALs predate it).
keywords: [wal, header, marker, format, version, tolerance, validation]
domains: [wal, format]
related: [wal-four-column-invariant, file-based-memory]
---

## Status history

- 2026-05-08 — proposed. All current readers already tolerate the
  marker (line splits to fewer than two `|`-separated fields and is
  skipped by the `len(fields) < 2` guard in
  `internal/profile/profile.go::collectWALSignals`, and by the same
  shape of guard in bash awk readers). `internal/wal.Validate`
  explicitly recognises the marker as a header on row 1 and skips
  the row instead of reporting it as a malformed event. Writer
  enablement is deferred to v1.2+ pending real-corpus observation
  of the marker's value.

## What

Reserve the line shape

```
# hypomnema-wal v2
```

as an **optional** first-line marker for `~/.claude/memory/.wal`. The
marker carries the canonical format version number identical to the
`format_version: 2` declaration in `docs/FORMAT.md`. When present,
it MUST be the very first line of the file.

Readers MUST:

- Tolerate the marker's presence on row 1 (skip without crashing).
- Tolerate the marker's absence (legacy WALs written before v1.x do
  not carry it).
- Reject the marker shape on any row other than row 1 — that's a
  real malformed row, not a header.

Writers (when implemented; see "When to revisit"):

- Write the marker on row 1 of a fresh WAL only. Existing WALs are
  left unchanged — back-fill is explicitly out of scope.
- Never overwrite a row that's already there (appending writers are
  forbidden from rewriting earlier rows).

## Why

1. **Distinguishing genuinely-malformed WAL from legacy WAL.**
   Without a header, a corrupted file (truncated mid-line, wrong
   encoding, copied from a different tool) parses as v1 silently —
   readers' tolerance for unknown event types swallows the corruption.
   With a header, readers can warn loudly: *if this looks like
   hypomnema's WAL, the marker should be there; if it isn't, treat
   the file with suspicion*.

2. **Forward-compatible version negotiation.** Format v3 (whenever
   that lands) needs a way to declare itself without breaking v2
   readers. A typed first-line marker is the natural channel: v2
   readers see `v2`, v3 readers see `v3`, both can refuse to parse
   versions they don't understand instead of silently producing
   wrong results.

3. **Cheap to implement, cheap to ignore.** The line split that
   every reader already does naturally rejects the marker as a
   malformed event (one column instead of four), and existing
   guards already skip such rows. No reader needs to change to
   accept the marker; only `wal.Validate` and any future explicit
   header-aware reader needs the special case.

## Trade-off accepted

- **Marker is "optional" in the strict sense.** Files without the
  marker remain valid v2 WAL. This means the marker cannot serve as
  a definitive "this file is mine" signal — only as a "this looks
  like mine + version is current" hint. We chose that intentionally:
  back-filling marker into existing user WALs would be a one-off
  migration with no real benefit (they're already v2 by content),
  and a strict requirement would force a version bump for a purely
  defensive feature.
- **One more line that pure-event-count metrics need to ignore.**
  Some downstream metrics that `wc -l .wal` would naively count
  now over-count by one if the marker is present. Mitigation: those
  metrics already filter to rows matching the four-column pattern;
  the marker's single-column shape is rejected by the existing
  filter. Verified for `internal/profile`, `bin/memory-self-profile.sh`,
  `hooks/wal-compact.sh`.
- **Adding the marker is a writer-side change that requires
  testing all writer paths.** Multiple writers append to `.wal`
  (`hooks/session-start.sh`, `hooks/session-stop.sh`,
  `internal/wal/Append`, etc.). Whichever writer ships marker
  emission first must coordinate with the others to avoid double-
  marker (each writer detecting an empty WAL and writing the
  header). Resolution: marker emission belongs to a single
  initialisation path — a one-shot `memoryctl wal init` subcommand
  or an installer-time check — not to the per-event append path.

## When to revisit

- After 30 days of `memoryctl wal validate` adoption, evaluate
  whether the marker's diagnostic value justifies writer enablement.
  Signal: how many real-corpus `memoryctl wal validate` runs
  reported confusing failures that a header would have made
  unambiguous? Below five over the window — defer writer indefinitely;
  above ten — schedule writer for the next minor release.

- If a format v3 grammar change is proposed before that window, the
  marker's role shifts from defensive to load-bearing (v3 readers
  must reject v2 input by header) and writer enablement becomes
  mandatory in the v3 release.

- Calendar 2027-05-08 — twelve months from proposed date. Even
  without observed pressure, revisit the cost/benefit then.

## Origin

Proposed alongside `memoryctl wal validate` (v1.1) to give the
validator a forward-compatible way to express "this file is current
format". The four-column invariant in
[`wal-four-column-invariant.md`](wal-four-column-invariant.md) tells
us *what a row looks like*; this ADR adds *what a file looks like*.
The two are independent: a v2 WAL can be a stream of v2 rows without
the marker, and the marker on its own does not change row grammar.

## Implementation pointer

This ADR is **proposed** until writer enablement lands. Activation
steps, in order:

1. `memoryctl wal init` subcommand — one-shot, idempotent. On a
   missing or empty `.wal`, write the marker as row 1. On a `.wal`
   that already has any content, no-op (do NOT prepend, do NOT
   overwrite).
2. `memoryctl wal validate` extension — when status flips to
   `active`, validate emits a WARN (not a failure) on a v2 WAL
   without the marker. WARN doesn't change exit code; it just
   surfaces the gap so user can `memoryctl wal init` if they
   want it.
3. Status: `proposed` → `active` once steps 1-2 land. ADR remains
   the spec source of truth for marker shape; the validator and
   init enforce it.
