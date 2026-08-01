# DATA-007 — Render benchmark corpus and baseline

This package is the named benchmark corpus of NFR-002: representative
**page**, **fragment** (inline and standalone), **nested-layout**
(children composition), and **route-binding** rendering workloads.
`baseline.json` records the measured figures, the calibration
constant, and the pinned templ version used for the one-time fork-time
reference measurement. (`benchmarks/render/` is a separate artifact:
the ad-hoc benchmark inherited from templ, not part of this gate.)

## The gate

`TestRenderRegressionGate` (env-gated by `GHTMX_PERF_GATE=1`, run in
the CI perf-gate job) enforces, per workload:

- **allocs/op** exactly equal to the baseline, and **B/op** at most 5%
  over it. These are the enforceable form of the 5% regression bound:
  they are deterministic (stable to under 0.01% across every recording
  session) and almost any real rendering-cost regression — extra
  writes, extra boxing, a lost buffer reuse — moves them.
- **wall-clock ratio** (workload ns/op over an adjacent stdlib
  mini-renderer calibration, minimum of 3 rounds) is measured and
  logged against the baseline every run, and fails the gate only past
  a **10× catastrophe backstop**.

### Why wall-clock is not gated at 5%

The 5% wall-clock gate was implemented three ways during task 62/63
bring-up — minimum-of-N absolute budgets, speed-calibrated budgets
(sha256 yardstick), and adjacent-ratio minima — and all three failed
their own freshly recorded baseline within minutes on shared
infrastructure: back-to-back sessions moved the yardstick itself by
12–49% and workload ratios by ±30%, while allocation figures never
moved by a single alloc. A 5% wall-clock bound on shared runners
guarantees flakes, not protection; the deterministic figures provide
the 5% protection NFR-002 is after, and the logged trend plus the 10×
backstop keep genuine collapses visible and fatal.

The residual blind spot is a non-allocating pure-CPU regression
between 1.05× and 10× — an added escaping pass, a quadratic scan. Two
future hardenings would close it: a deterministic operation-count
metric (`B.ReportMetric` over counted writes), or — on dedicated
hardware, or if "never a live comparison" is read as "never against
templ" — a paired same-session benchstat run of the baseline commit
against the change, the only statistically sound 5% wall-clock design
on shared runners.

The gate reads `baseline.json` and nothing else. It never measures
another project: the templ figures in the record are the one-time
fork-time reference, kept for context only.

## Fork-time reference (informational)

Measured 2026-08-01 on the same machine against templ
`04abee5364c6fab2bde8c00d215fdcb630ad6a94` (v0.3.1020) with an
equivalent `.templ` corpus (bindings and nested layouts have no
upstream equivalent). Both columns below use the reference procedure —
fixed `-benchtime 2000x`, minimum of 5 runs — which differs from the
gate's estimator, so the ghtmx ns/op column here is not comparable to
`baseline.json`'s figures; only the two columns of this table compare
with each other:

| Workload | templ ns/op | ghtmx ns/op | templ allocs | ghtmx allocs |
| --- | --- | --- | --- | --- |
| Page | 570,530 | 271,540 | 1,210 | 1,210 |
| Fragment (inline) | 328,012 | 120,410 | 905 | 705 |
| Fragment (standalone) | 3,881 | 1,488 | 10 | 10 |
| Bindings | — | 59,655 | — | 305 |

The fragment inline improvement (705 vs 905 allocs) is structural: the
compile-time shared-body design renders nested fragments as direct
calls instead of component values.

## Revising the baseline

A baseline revision is a deliberate, reviewed act:

1. Re-measure on a quiet machine:
   `GHTMX_RECORD_BASELINE=1 go test ./benchmarks/corpus/ -run TestRecordBaseline -v`
   prints fresh figures in `baseline.json` form.
2. Update every field of `baseline.json` in one commit, including
   `recorded_at`, `machine` (with the Go version — the perf-gate CI
   job pins it, so bump both together), `calibration_ns_op`, and a
   `revision_justification` that says *why* the figures moved.
   Reviewers judge the justification; the gate only enforces that one
   exists.
3. The gate fails on an empty justification and on a workload-count
   mismatch, so a partial or silent revision cannot land.
