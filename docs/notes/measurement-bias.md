# Measurement bias: open behavioural quanta

**Status:** documented, not fixed. `memoryctl doctor` surfaces the metric as a WARN when the open ratio exceeds 50% in the last 30 days.

## What an open quantum is

A *behavioural quantum* in hypomnema is one Claude Code session: SessionStart injects some set of memory files, the user prompts (maybe matching trigger records along the way), and Stop runs a closing pass that labels each inject as `trigger-useful` or `trigger-silent`, plus `outcome-positive` / `outcome-negative` for mistakes that either didn't repeat or did.

A quantum is **closed** when at least one posterior event is recorded against the session's `session_id`. It is **open** when an inject event exists for that session but no closing event does.

Open quanta are dead weight for metrics: the Bayesian `eff = (pos + 1) / (pos + neg + 2)` that `memory-analytics.sh` uses to classify records as `winner` / `noise` computes over whatever `trigger-useful` / `trigger-silent` fires, not over the full volume of injects. Every inject that never got classified is an observation silently dropped from the denominator.

## Measured on the author's corpus

Run on 2026-04-23, 30-day window (excludes pre-v0.7.1 entries so the number reflects current evidence-match logic, not the slug-grep that never fired):

```
$ memoryctl doctor | grep open_quanta
[WARN] open_quanta_last_30d  210/226 sessions without closing signal (93%);
                             1716 injects unclassified in last 30d
```

Narrower post-v0.7.1 slice (since 2026-04-15):

```
Sessions since 2026-04-15 with ≥1 inject : 39
  Stop hook reached evidence-match pass (≥1 trigger-useful/silent): 9 (23.1%)
  Stop reached outcome-detect but NOT evidence-match              : 1
  Stop reached clean-session only                                 : 0
  No Stop signal at all (crash / <120s / no transcript)           : 29 (74.4%)
    └ injects in those sessions, never classified                : 312
```

So on a real install approximately three quarters of sessions that carry injects leave no trace in the Stop-side WAL at all. Those 312 injects (77% of the recent-window total) live in `eff` as if they never existed.

## Why Stop doesn't fire

Three known causes, none of them bugs per se — they're properties of the Claude Code lifecycle and the hook contract hypomnema signed up for:

1. **`MIN_SESSION_SECONDS = 120`.** Sessions shorter than two minutes skip the Stop body entirely (session-stop.sh early-exits). Short sessions are common — spawn a subagent for a one-shot lookup, Ctrl-C a prompt that went sideways, open a session to paste a URL.

2. **Missing `transcript_path`.** `session-stop.sh` guards evidence extraction on the presence of the transcript JSONL. When Claude Code doesn't pass one (agent sessions, some CLI flows), the evidence pass is skipped — no trigger-useful / trigger-silent can fire, no outcome-positive can be derived.

3. **Hard exits.** Ctrl-C'd sessions, crashes, and sessions Claude Code tears down abnormally never reach Stop.

## Side finding: session-metrics is missing session_id

While investigating this I noticed that `hooks/session-stop.sh:450` writes `session-metrics` as:

```
TODAY|session-metrics|DOMAINS|error_count:X,tool_calls:Y,duration:Zs
```

docs/FORMAT.md §5.1 permits the third column to be a metrics-pairs string (`"key:value pairs"`), but §5.2 declares the four-column `date|event|target|session_id` layout "immutable within format v1" — and this event uses column 4 for metrics instead of `session_id`. The `session_id` is dropped on the floor.

`clean-session` writes correctly (column 4 = session_id). `session-metrics` is the only defined event that violates the position.

Consequence: `session-metrics` can't be used to tell whether a given session reached Stop — it can't be joined to inject events. For the open-quanta metric this means we fall back to `clean-session` as the only Stop-side signal carrying `session_id`, which is written less often (only when error_count == 0).

Fixing this is a format-level change: either widen the grammar (breaking) or reinterpret column 3 (reinterpreting events that have already been written). Both want a v1→v2 version bump. Filed here rather than patched.

## What hypomnema does about it today

Nothing corrective. The measurement runs on the sample it has. Three mitigations keep the damage bounded:

- Laplace smoothing (`+1` / `+2`) in `eff` makes a one-observation record produce `eff ≈ 0.67` rather than 1.0 — a single lucky `trigger-useful` doesn't crown a file as `winner`.
- The `winner` threshold (`eff > 0.7` by default) combined with `MIN_INJECTS_WINNER = 5` requires at least five `trigger-*` events before a record is labelled, which open quanta don't contribute.
- `memoryctl doctor` surfaces the ratio so operators notice.

## What might help later (not committed)

In rough effort order:

1. **Retroactive silent classification in `wal-compact.sh`.** Sessions older than 24h with inject events but no closing signal: emit `trigger-silent` for each inject, tagged with the original date. Restores the Bayesian denominator. Requires a design decision about whether to also emit `outcome-positive` for non-repeated mistakes in the same shape.

2. **Fix `session-metrics` wire format.** v1→v2 format bump, adds `session_id` back to column 4 and moves metrics into column 3's structured-string slot alongside `clean-session`'s domain.

3. **Track an explicit `session-abandoned` WAL event.** A nightly or on-next-SessionStart pass writes it for sessions whose marker (`/tmp/.claude-session-<id>`) is older than the timeout. Gives analytics something to count that's different from "legitimately classified silent".

4. **Lower `MIN_SESSION_SECONDS`.** Currently 120s; maybe 60s is enough to filter pure spawn-and-exit sessions while catching more legitimate short interactions. Needs a look at the distribution of `duration:Ns` entries in the metrics WAL before changing.

These are independent. #1 is the highest-leverage one because it directly fixes the Bayesian sample; #2 is a pure hygiene fix that unlocks better session-level analytics; #3 is observational; #4 is parameter-tuning.

## History

- Open-quanta concept surfaced in the 2026-04-23 TFS audit (Phase 6) as a "patological quant" — formally, a session whose causal-loop doesn't close.
- Metric implemented as `doctor.checkOpenQuanta` on 2026-04-23. Author corpus at that date: 93% open in 30d window, 74% open in post-v0.7.1 window.
- `session-metrics` contract violation found during the same audit pass; logged here rather than silently fixed.
