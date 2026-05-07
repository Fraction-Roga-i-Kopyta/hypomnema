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

## Stop-hook variance

`stop (3 WAL passes + evidence)` stayed observational because the
local 3-run capture spread `1901 / 2912 / 4929 ms` (max/min ratio
≈ 2.6×) against an avg of 2912 ms. CI numbers landed cleaner
(macOS 3517 ms, Linux 1641 ms — both single-sample), but until
multi-run data shows the real distribution holds < `1.5×`
spread, gating the scenario would flake on the high tail. Revisit
once the baseline JSON has a documented multi-run mean.
