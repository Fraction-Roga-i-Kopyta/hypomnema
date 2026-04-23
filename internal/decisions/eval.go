package decisions

import (
	"fmt"
)

// Evaluate applies every trigger to the snapshot and returns one
// Result per trigger, in input order. Pure function — no I/O, no
// time.Now(), no env vars. Caller supplies `snap.Today` already
// formatted; calendar triggers compare strings lexicographically
// (YYYY-MM-DD is order-preserving).
//
// Slug is attached to each Result so the caller can render grouped
// output without tracking origin separately.
func Evaluate(triggers []Trigger, slug string, snap Snapshot) []Result {
	out := make([]Result, 0, len(triggers))
	for _, t := range triggers {
		out = append(out, evaluateOne(t, slug, snap))
	}
	return out
}

func evaluateOne(t Trigger, slug string, snap Snapshot) Result {
	if t.IsCalendar() {
		return evaluateCalendar(t, slug, snap)
	}
	return evaluateMetric(t, slug, snap)
}

func evaluateCalendar(t Trigger, slug string, snap Snapshot) Result {
	// YYYY-MM-DD lexicographic comparison == chronological comparison
	// (ISO-8601 property). Fires when today ≥ after.
	if snap.Today >= t.After {
		days := daysBetween(t.After, snap.Today)
		return Result{
			Slug:    slug,
			Trigger: t,
			Status:  StatusOverdue,
			Message: fmt.Sprintf("after %s passed (%d days ago)", t.After, days),
		}
	}
	return Result{
		Slug:    slug,
		Trigger: t,
		Status:  StatusOK,
		Message: fmt.Sprintf("after %s in %d days", t.After, daysBetween(snap.Today, t.After)),
	}
}

func evaluateMetric(t Trigger, slug string, snap Snapshot) Result {
	value, ok := resolveMetric(t, slug, snap)
	if !ok {
		return Result{
			Slug:    slug,
			Trigger: t,
			Status:  StatusSkipped,
			Message: fmt.Sprintf("metric %s not available in snapshot", t.Metric),
		}
	}

	fired := compare(value, t.Operator, t.Threshold)
	status := StatusOK
	if fired {
		status = StatusPressure
	}
	return Result{
		Slug:    slug,
		Trigger: t,
		Status:  status,
		Message: fmt.Sprintf("%s = %s %s %s",
			t.Metric,
			formatMetric(value),
			t.Operator,
			formatMetric(t.Threshold)),
	}
}

// resolveMetric pulls the right value from the snapshot. `source:
// file` + `metric: ref_count` is special-cased — the value is
// per-slug. Everything else lives in Snapshot.Metrics.
func resolveMetric(t Trigger, slug string, snap Snapshot) (float64, bool) {
	if t.Source == "file" && t.Metric == "ref_count" {
		v, ok := snap.FileRefCount[slug]
		if !ok {
			return 0, false
		}
		return float64(v), true
	}
	v, ok := snap.Metrics[t.Metric]
	if !ok {
		return 0, false
	}
	return v, true
}

func compare(a float64, op string, b float64) bool {
	switch op {
	case "<":
		return a < b
	case "<=":
		return a <= b
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "==":
		return a == b
	case "!=":
		return a != b
	}
	// Unknown operator: be conservative — don't claim pressure on a
	// field we can't interpret. parse.go rejects unknown operators at
	// read time, so this branch is defence-in-depth.
	return false
}

// formatMetric renders floats without trailing zeros when the value is
// an integer (e.g., ref_count=5 should print "5", not "5.0000"); keeps
// the fraction for genuine fractions (precision=0.33).
func formatMetric(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.2f", v)
}

// daysBetween returns the absolute difference in days between two
// YYYY-MM-DD strings. Returns 0 on parse error — the caller's message
// just omits the count in that edge case rather than bubbling the
// error (review is advisory; a broken date shouldn't fail the run).
func daysBetween(a, b string) int {
	ta, errA := parseISODate(a)
	tb, errB := parseISODate(b)
	if errA != nil || errB != nil {
		return 0
	}
	d := tb - ta
	if d < 0 {
		d = -d
	}
	return d / 86400
}
