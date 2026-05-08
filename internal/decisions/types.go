// Package decisions implements ADR review-triggers evaluation.
//
// An ADR ships with `review-triggers:` in its frontmatter — a block
// sequence of metric/operator/threshold/source tuples plus optional
// calendar `after:` entries. The evaluator reads those triggers, pulls
// the current values from a Snapshot (self-profile + WAL), and returns
// per-trigger Results that the `memoryctl decisions review` command
// formats for the operator.
//
// The package is deliberately split into three surfaces:
//
//   - types.go   — pure data. No I/O, no external deps.
//   - eval.go    — pure function: (triggers, snapshot) → results.
//   - parse.go   — reads an ADR file, extracts Trigger list.
//   - snapshot.go — builds Snapshot from self-profile.md + WAL.
//
// Everything under eval.go and types.go must stay side-effect-free so
// the evaluator is trivially fixture-testable — the v0.11 plan names
// "deterministic metric eval only" as a non-goal for LLM-in-the-loop,
// and we enforce that by construction.
package decisions

// Trigger is one entry from an ADR's `review-triggers:` block.
// Exactly one of the two shapes is populated per Trigger instance:
//
//   Metric-based:  Metric + Operator + Threshold + Source set; After empty.
//   Calendar:      After set (YYYY-MM-DD); all other fields empty.
//
// parse.go enforces the shape; Evaluate assumes the invariant.
type Trigger struct {
	Metric    string  // "measurable_precision", "recurrence_top_mistake", etc.
	Operator  string  // "<", "<=", ">", ">=", "==", "!="
	Threshold float64 // numeric threshold — mistakes/ recurrence is count, treated as float for operator uniformity
	Source    string  // "self-profile" | "wal" | "file"
	After     string  // YYYY-MM-DD for calendar triggers (mutually exclusive with the metric fields)

	// Direction marks the semantics of the firing condition:
	//   "" or "below" — crossing is degradation; emits StatusPressure
	//                   (the original behaviour, preserved by leaving
	//                   Direction empty in pre-existing ADRs).
	//   "above"       — crossing is a positive milestone; emits
	//                   StatusReached. Used by ADRs whose review-trigger
	//                   marks a goal being met, not a regression
	//                   (e.g. intuition-milestone). Set Direction
	//                   intentionally — defaulting unknown to "below"
	//                   preserves backward compatibility.
	Direction string // "above" | "below" | ""
}

// IsCalendar returns true when this is an `after:`-only trigger.
func (t Trigger) IsCalendar() bool { return t.After != "" }

// Status is the three-level outcome of evaluating one trigger against
// one snapshot.
type Status int

const (
	// StatusOK — the trigger did not fire. No action.
	StatusOK Status = iota
	// StatusPressure — the metric crossed its threshold and the
	// crossing is a degradation signal (Direction = "below" or
	// unset). Author should re-read the ADR's "When to revisit"
	// section.
	StatusPressure
	// StatusOverdue — a calendar `after:` date has passed.
	StatusOverdue
	// StatusSkipped — trigger metric missing from the snapshot (older
	// self-profile format, field renamed, etc.). Emits a WAL event but
	// does not fail the review.
	StatusSkipped
	// StatusReached — the metric crossed its threshold and the
	// crossing is a goal being met (Direction = "above"). Distinct
	// from StatusPressure so positive milestones (intuition-milestone,
	// future "corpus matured" markers) read correctly in `decisions
	// review` output and don't trip degradation-flavoured alerting.
	StatusReached
)

// String renders status for CLI output and tests.
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusPressure:
		return "pressure"
	case StatusOverdue:
		return "overdue"
	case StatusSkipped:
		return "skipped"
	case StatusReached:
		return "reached"
	}
	return "?"
}

// Result pairs a trigger with its evaluation outcome. Slug identifies
// the source ADR so downstream formatting can group by file.
type Result struct {
	Slug    string
	Trigger Trigger
	Status  Status
	// Message is a one-liner for operator display. Example:
	//   "shadow_miss_ratio = 0.34 > 0.30"
	//   "after 2026-07-22 passed (14 days ago)"
	//   "metric measurable_precision not available in snapshot"
	Message string
}

// Snapshot is the set of metric values the evaluator consults.
// Populated by BuildSnapshot (snapshot.go); filtered subset passed to
// Evaluate in tests. An absent metric (zero value + present=false in
// the by-name map) routes the corresponding trigger to StatusSkipped.
type Snapshot struct {
	Today string // YYYY-MM-DD; used for calendar trigger comparison.

	// Metrics holds every numeric observation addressable by the
	// `metric:` field. Presence matters, not just value — a metric
	// absent from the map means the snapshot couldn't compute it, and
	// Evaluate responds with StatusSkipped rather than defaulting to
	// zero (which would spuriously fire `<` operators).
	Metrics map[string]float64

	// FileRefCount is a sidecar for the `source: file` metric
	// `ref_count` — indexed by slug so the evaluator can pick up the
	// ADR's own ref_count without re-reading frontmatter.
	FileRefCount map[string]int
}
