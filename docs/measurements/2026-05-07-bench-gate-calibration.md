# Bench-gate baseline calibration (2026-05-07)

First CI-driven measurement of the perf gate landed in PR #6.
This note records the baseline numbers in
`docs/measurements/baselines.json`, the data used to seed them,
and the calibration headroom on each runner so a future tightening
pass has a reference point.

## Method

- `hooks/bench-memory.sh perf` builds a 500-file synthetic corpus
  in a tmpdir and times each hot-path hook over 3–5 runs per
  scenario.
- `scripts/bench-gate.sh` parses the per-scenario `avg=Nms` lines
  and compares against `baselines.json[<scenario>].<platform>.avg_ms × tolerance`.
- Local seed values came from a single `bash hooks/bench-memory.sh perf`
  pass on macOS darwin 25.x, M-series, on commit `a4467ac`.
- First CI measurements: PR #6 run
  [25520669238](https://github.com/Fraction-Roga-i-Kopyta/hypomnema/actions/runs/25520669238).

## Initial baselines + first CI observations

| Scenario                              | Gated | darwin baseline | macOS CI obs | macOS ratio | linux baseline | linux CI obs | linux ratio |
| ------------------------------------- | :---: | --------------: | -----------: | ----------: | -------------: | -----------: | ----------: |
| session-start                         |   ✓   |          600 ms |       708 ms |       1.18× |        1200 ms |       442 ms |       0.37× |
| user-prompt-submit (no-match)         |   ✓   |         3000 ms |      3727 ms |       1.24× |        5000 ms |      2378 ms |       0.48× |
| user-prompt-submit (broad)            |   ✓   |         3500 ms |      3521 ms |       1.01× |        6000 ms |      2817 ms |       0.47× |
| stop (3 WAL passes + evidence)        |       |         4000 ms |      3517 ms |       0.88× |        6000 ms |      1641 ms |       0.27× |
| secrets-detect (0 patterns)           |   ✓   |           50 ms |        47 ms |       0.94× |         100 ms |        26 ms |       0.26× |
| secrets-detect (10 patterns)          |   ✓   |           50 ms |        50 ms |       1.00× |         100 ms |        26 ms |       0.26× |
| secrets-detect (100 patterns)         |   ✓   |           60 ms |        59 ms |       0.98× |         120 ms |        28 ms |       0.23× |

## Headroom analysis

**macOS** (one CI sample): every gated scenario lands inside
`1.0×–1.25×` of its baseline. Tolerance `1.5×` leaves 20–50 ms
of buffer per scenario before the gate flips. Reasonable —
runner variance on hosted-mac is well-known; tightening below
`1.3×` would risk flake.

**Linux** (one CI sample): every gated scenario lands at
`0.23×–0.48×` of its baseline. The Linux baselines were initial
estimates (≈ 2× darwin) on the assumption that Ubuntu runners
spend more time in syscalls than M-series macs; the real numbers
say Ubuntu runners are roughly *as fast as* the maintainer's
local M-series for these workloads. Current threshold leaves
3–4× headroom, which would mask all but catastrophic regressions.

## Recommended tightening

Worth a follow-up PR after 5–10 more CI rounds populate a real
distribution. Conservative target: re-seed Linux baselines from
the median of those rounds × `1.2`, i.e.

| Scenario                              | Linux current | Linux suggested |
| ------------------------------------- | ------------: | --------------: |
| session-start                         |       1200 ms |          550 ms |
| user-prompt-submit (no-match)         |       5000 ms |         2900 ms |
| user-prompt-submit (broad)            |       6000 ms |         3400 ms |
| secrets-detect (0 patterns)           |        100 ms |           35 ms |
| secrets-detect (10 patterns)          |        100 ms |           35 ms |
| secrets-detect (100 patterns)         |        120 ms |           40 ms |

These keep `1.5×` tolerance but cut headroom from 3–4× to ≈ 1.5×,
restoring the gate's signal on Linux. Wait for more samples
before applying — a single CI run is not a distribution.

## Same-day re-calibration: darwin baselines bumped

Within hours of the first CI capture, PR #8 (a doc-only changelog
expansion, no code changes) hit a darwin gate failure with
session-start at **1302 ms** vs the 600 ms × 1.5 = 900 ms ceiling.
Two macOS samples on the same code:

| Sample      | session-start | user-prompt-submit (broad) |
| ----------- | ------------: | -------------------------: |
| PR #6 run   |        708 ms |                    3521 ms |
| PR #8 run   |       1302 ms |                    6842 ms |
| ratio (PR8 / PR6) | 1.84× | 1.94× |

Same code, ~2× spread on the bash-heavy hot-paths between two
runs minutes apart. This is the macOS hosted-runner variance the
v0.14 measurements doc already warned about; the initial darwin
baselines (600 / 3000 / 3500 ms) were calibrated against the
maintainer's local M-series Mac, which is materially faster than
hosted macos-latest.

darwin baselines moved to **hosted-runner ceilings** so the gate
passes on the runner that actually runs CI:

| Scenario                              | Old darwin baseline | New darwin baseline |
| ------------------------------------- | ------------------: | ------------------: |
| session-start                         |              600 ms |             1000 ms |
| user-prompt-submit (no-match)         |             3000 ms |             5000 ms |
| user-prompt-submit (broad)            |             3500 ms |             5500 ms |

Tolerance stays at 1.5×; ceilings now sit at 1500 / 7500 / 8250 ms,
covering the worst PR #8 sample with headroom. Local M-series numbers
(520–2940 ms) sit at 0.52–0.53× of baseline — that's the price of
calibrating to the slower of the two environments. The cleaner
long-term answer would be ratio-vs-main comparison, but that's
2× CI time and not worth the complexity until single-baseline
gating proves insufficient.

## Same-day re-calibration (round 2): secrets-detect bumped

Post-merge run #25521696126 on main flunked two more darwin
scenarios: `secrets-detect (10 patterns)` at **78 ms** (ceiling
75 ms) and `(100 patterns)` at **91 ms** (ceiling 90 ms). Three
samples on the same code:

| Scenario       | PR #6 | PR #8 first | Post-#8 main |
| -------------- | ----: | ----------: | -----------: |
| (0 patterns)   | 47 ms |       63 ms |        71 ms |
| (10 patterns)  | 50 ms |       67 ms |    **78 ms** |
| (100 patterns) | 59 ms |       69 ms |    **91 ms** |

The numbers crept up across runs — same pattern of hosted-darwin
variance the session-start hot-paths showed earlier. The
original 50 / 50 / 60 ms baselines were calibrated against the
local M-series Mac where the hook completes in ~33 ms; hosted
macos-latest sits comfortably 1.4–1.6× above local. Bumping
darwin baselines to **70 / 70 / 80 ms** (ceilings 105 / 105 /
120 ms) covers the worst-observed sample with ~15 ms of
headroom. Tolerance stays at 1.5×. Linux numbers (28–30 ms) are
unchanged — far below their already-generous ceilings.

## Stop-hook variance

`stop (3 WAL passes + evidence)` stayed observational because the
local 3-run capture spread `1901 / 2912 / 4929 ms` (max/min ratio
≈ 2.6×) against an avg of 2912 ms. CI numbers landed cleaner
(macOS 3517 ms, Linux 1641 ms — both single-sample), but until
multi-run data shows the real distribution holds < `1.5×`
spread, gating the scenario would flake on the high tail. Revisit
once the baseline JSON has a documented multi-run mean.

## Same-day re-calibration (round 3, 2026-05-08): bash hot-paths bumped again

PR #10 (a docs-only removal of two markdown files; zero hot-path
code touched) hit two flakes on darwin within two consecutive runs:

| Sample            | session-start | user-prompt-submit (no-match) |
| ----------------- | ------------: | ----------------------------: |
| Run 1 (`bj6lir5fu`) |       1981 ms |                       8349 ms |
| Run 2 (rerun)     |       1575 ms |                       passed  |

Run 1 broke both gated bash-heavy hot-paths; run 2 broke only
session-start (1.57× of the 1500 ms ceiling). Code identical between
runs — pure hosted-runner variance, same shape as round 1 / round 2
documented above. The post-round-2 ceilings (1500 / 7500 ms) sit
*at the centroid* of the observed range, not above it; round 3
moves the centroid up so the ceiling sits above the worst observed
sample with headroom.

| Scenario                              | Round-2 darwin baseline | Round-3 darwin baseline | New ceiling | Worst observed |
| ------------------------------------- | ----------------------: | ----------------------: | ----------: | -------------: |
| session-start                         |                 1000 ms |                 1500 ms |     2250 ms |        1981 ms |
| user-prompt-submit (no-match)         |                 5000 ms |                 6000 ms |     9000 ms |        8349 ms |

Tolerance stays 1.5×. Headroom: session-start 269 ms (~14 %),
user-prompt-submit 651 ms (~7 %). Linux numbers untouched — they
sit comfortably below their already-generous ceilings.

This is the third re-calibration in 36 h on the same hot-paths.
The pattern — local M-series numbers ~50 % of CI, run-to-run CI
variance ~2× even minutes apart — is now documented across three
data points. If round 4 follows the same template (gate fails on
darwin, code didn't change), the right move is no longer baseline
bumping but switching to ratio-vs-main comparison or sampling >3
runs to smooth the tail. Park as v1.x followup if it recurs.

## Same-day re-calibration (round 4, 2026-05-08): user-prompt-submit (broad) bumped

PR #16 (a docs-only addition: `docs/EVENTS.md` registry) hit the
darwin gate at `user-prompt-submit (broad)` = **8651 ms** vs the
round-3 ceiling 5500 ms × 1.5 = 8250 ms (ratio 1.57×). Code path
identical; same hosted-runner variance pattern as rounds 1-3, just
hitting a different scenario.

| Scenario                              | Round-3 darwin baseline | Round-4 darwin baseline | New ceiling | Worst observed |
| ------------------------------------- | ----------------------: | ----------------------: | ----------: | -------------: |
| user-prompt-submit (broad)            |                 5500 ms |                 6500 ms |     9750 ms |        8651 ms |

Tolerance unchanged at 1.5×. Linux baselines untouched.

This is the **fourth** baseline bump in 48 h. The round-3 closing
note named ratio-vs-main / multi-sample as the right architectural
fix at this point. **Decision after round 4:** ratio-vs-main is
deferred to v1.x as a focused work item — it doubles CI runtime
(needs a `main` reference run) and the implementation isn't a
copy-paste of an existing pattern. Sample-count smoothing
(`hooks/bench-memory.sh perf` runs ≥5 instead of 3 per scenario,
take p50 not avg) is the smaller intermediate step worth piloting
first; tracked in maintainer's local memory under v1.x followups.

Until that lands, future round-N bumps follow this template
unchanged. The cost is calibration-debt growing (each bump moves
the gate further from real-machine numbers); the benefit is the
gate stays signal-bearing for genuinely large regressions while
absorbing runner variance.
