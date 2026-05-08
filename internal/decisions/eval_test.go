package decisions

import (
	"strings"
	"testing"
)

func TestEvaluate_MetricPressureLt(t *testing.T) {
	snap := Snapshot{
		Today: "2026-04-23",
		Metrics: map[string]float64{
			"measurable_precision": 0.35,
		},
	}
	tr := Trigger{
		Metric: "measurable_precision", Operator: "<", Threshold: 0.40,
		Source: "self-profile",
	}
	results := Evaluate([]Trigger{tr}, "sub-triggers", snap)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Status != StatusPressure {
		t.Errorf("status: got %v, want pressure", r.Status)
	}
	if !strings.Contains(r.Message, "0.35") || !strings.Contains(r.Message, "< 0.40") {
		t.Errorf("message missing value/threshold: %q", r.Message)
	}
}

func TestEvaluate_MetricOKWhenOutsideBand(t *testing.T) {
	snap := Snapshot{
		Metrics: map[string]float64{"measurable_precision": 0.80},
	}
	tr := Trigger{
		Metric: "measurable_precision", Operator: "<", Threshold: 0.40,
		Source: "self-profile",
	}
	results := Evaluate([]Trigger{tr}, "ok-adr", snap)
	if results[0].Status != StatusOK {
		t.Errorf("expected StatusOK, got %v", results[0].Status)
	}
}

func TestEvaluate_MetricMissing(t *testing.T) {
	snap := Snapshot{Metrics: map[string]float64{}}
	tr := Trigger{
		Metric: "measurable_precision", Operator: "<", Threshold: 0.40,
		Source: "self-profile",
	}
	results := Evaluate([]Trigger{tr}, "any", snap)
	if results[0].Status != StatusSkipped {
		t.Errorf("expected StatusSkipped on missing metric, got %v", results[0].Status)
	}
	if !strings.Contains(results[0].Message, "not available") {
		t.Errorf("message should say metric missing, got %q", results[0].Message)
	}
}

func TestEvaluate_CalendarOverdue(t *testing.T) {
	snap := Snapshot{Today: "2026-08-05"}
	tr := Trigger{After: "2026-07-22"}
	results := Evaluate([]Trigger{tr}, "bash-go-gradual-port", snap)
	if results[0].Status != StatusOverdue {
		t.Errorf("expected StatusOverdue, got %v", results[0].Status)
	}
	if !strings.Contains(results[0].Message, "14 days ago") {
		t.Errorf("expected '14 days ago' in message, got %q", results[0].Message)
	}
}

func TestEvaluate_CalendarFuture(t *testing.T) {
	snap := Snapshot{Today: "2026-04-23"}
	tr := Trigger{After: "2026-07-22"}
	results := Evaluate([]Trigger{tr}, "any", snap)
	if results[0].Status != StatusOK {
		t.Errorf("expected StatusOK for future date, got %v", results[0].Status)
	}
}

func TestEvaluate_FileRefCount(t *testing.T) {
	snap := Snapshot{
		FileRefCount: map[string]int{"decision-a": 42},
	}
	tr := Trigger{
		Metric: "ref_count", Operator: ">=", Threshold: 20,
		Source: "file",
	}
	results := Evaluate([]Trigger{tr}, "decision-a", snap)
	if results[0].Status != StatusPressure {
		t.Errorf("expected StatusPressure on ref_count >= 20 with value 42, got %v", results[0].Status)
	}
}

func TestEvaluate_AllOperators(t *testing.T) {
	snap := Snapshot{Metrics: map[string]float64{"x": 10}}
	cases := []struct {
		op   string
		th   float64
		want Status
	}{
		{"<", 20, StatusPressure},
		{"<", 5, StatusOK},
		{"<=", 10, StatusPressure},
		{">", 5, StatusPressure},
		{">", 20, StatusOK},
		{">=", 10, StatusPressure},
		{"==", 10, StatusPressure},
		{"==", 11, StatusOK},
		{"!=", 11, StatusPressure},
		{"!=", 10, StatusOK},
	}
	for _, c := range cases {
		tr := Trigger{Metric: "x", Operator: c.op, Threshold: c.th, Source: "self-profile"}
		got := Evaluate([]Trigger{tr}, "s", snap)[0].Status
		if got != c.want {
			t.Errorf("op %s th %v: got %v, want %v", c.op, c.th, got, c.want)
		}
	}
}

// --- parse tests ---

func TestParseTriggers_MixedShapes(t *testing.T) {
	src := `---
type: decision
review-triggers:
  - metric: measurable_precision
    operator: "<"
    threshold: 0.40
    source: self-profile
  - metric: shadow_miss_ratio
    operator: ">"
    threshold: 0.30
    source: wal
  - after: "2026-07-22"
---
body
`
	triggers, err := parseTriggersFromFrontmatter(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 3 {
		t.Fatalf("expected 3 triggers, got %d: %+v", len(triggers), triggers)
	}
	if triggers[0].Metric != "measurable_precision" || triggers[0].Threshold != 0.40 {
		t.Errorf("first trigger: %+v", triggers[0])
	}
	if !triggers[2].IsCalendar() || triggers[2].After != "2026-07-22" {
		t.Errorf("third trigger should be calendar: %+v", triggers[2])
	}
}

func TestParseTriggers_NoReviewTriggersField(t *testing.T) {
	src := `---
type: decision
status: active
---
body
`
	triggers, err := parseTriggersFromFrontmatter(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 0 {
		t.Errorf("expected 0 triggers, got %d", len(triggers))
	}
}

func TestParseTriggers_InvalidOperatorFails(t *testing.T) {
	src := `---
review-triggers:
  - metric: foo
    operator: "~="
    threshold: 1
    source: self-profile
---
`
	_, err := parseTriggersFromFrontmatter(strings.NewReader(src))
	if err == nil {
		t.Error("expected parse error for unknown operator")
	}
}

func TestParseTriggers_CalendarWithExtraFieldsFails(t *testing.T) {
	src := `---
review-triggers:
  - after: "2026-07-22"
    metric: foo
    operator: "<"
    source: self-profile
---
`
	_, err := parseTriggersFromFrontmatter(strings.NewReader(src))
	if err == nil {
		t.Error("expected error when calendar trigger also sets metric fields")
	}
}

func TestParseTriggers_UnknownSourceFails(t *testing.T) {
	src := `---
review-triggers:
  - metric: foo
    operator: "<"
    threshold: 1
    source: telemetry
---
`
	_, err := parseTriggersFromFrontmatter(strings.NewReader(src))
	if err == nil {
		t.Error("expected error for unknown source")
	}
}

func TestParseTriggers_QuotedAndBareValues(t *testing.T) {
	src := `---
review-triggers:
  - metric: measurable_precision
    operator: <
    threshold: 0.40
    source: self-profile
---
`
	triggers, err := parseTriggersFromFrontmatter(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if triggers[0].Operator != "<" {
		t.Errorf("bare-operator parse: got %q, want <", triggers[0].Operator)
	}
}

// --- direction (above/below) — new in v1.1 -----------------------

// TestEvaluate_DirectionAbove_FiringEmitsReached pins the contract for
// positive milestone triggers. When Direction == "above" and the
// metric crosses, status MUST be StatusReached, not StatusPressure.
// See ADR intuition-milestone.md.
func TestEvaluate_DirectionAbove_FiringEmitsReached(t *testing.T) {
	tr := Trigger{
		Metric:    "silent_applied_to_useful_ratio_30d",
		Operator:  ">",
		Threshold: 1.0,
		Source:    "self-profile",
		Direction: "above",
	}
	snap := Snapshot{
		Metrics: map[string]float64{"silent_applied_to_useful_ratio_30d": 1.42},
	}
	r := Evaluate([]Trigger{tr}, "intuition-milestone", snap)[0]
	if r.Status != StatusReached {
		t.Errorf("direction=above + crossed: got %v, want StatusReached", r.Status)
	}
	if !strings.Contains(r.Message, "✓ reached") {
		t.Errorf("reached message should carry ✓ marker, got %q", r.Message)
	}
}

func TestEvaluate_DirectionAbove_NotCrossedEmitsOK(t *testing.T) {
	tr := Trigger{
		Metric:    "silent_applied_to_useful_ratio_30d",
		Operator:  ">",
		Threshold: 1.0,
		Source:    "self-profile",
		Direction: "above",
	}
	snap := Snapshot{
		Metrics: map[string]float64{"silent_applied_to_useful_ratio_30d": 0.32},
	}
	r := Evaluate([]Trigger{tr}, "intuition-milestone", snap)[0]
	if r.Status != StatusOK {
		t.Errorf("direction=above + not crossed: got %v, want StatusOK", r.Status)
	}
}

// TestEvaluate_DirectionUnsetEmitsPressure preserves backward
// compatibility — every pre-direction ADR has Direction = "" and must
// continue to emit Pressure on crossing.
func TestEvaluate_DirectionUnsetEmitsPressure(t *testing.T) {
	tr := Trigger{
		Metric:    "shadow_miss_ratio",
		Operator:  ">",
		Threshold: 0.30,
		Source:    "self-profile",
	}
	snap := Snapshot{Metrics: map[string]float64{"shadow_miss_ratio": 0.42}}
	r := Evaluate([]Trigger{tr}, "fts5-shadow-retrieval", snap)[0]
	if r.Status != StatusPressure {
		t.Errorf("direction unset + crossed: got %v, want StatusPressure", r.Status)
	}
}

// TestParseTriggers_DirectionField checks that `direction:` parses
// out of frontmatter into Trigger.Direction.
func TestParseTriggers_DirectionField(t *testing.T) {
	src := `---
review-triggers:
  - metric: silent_applied_to_useful_ratio_30d
    operator: ">"
    threshold: 1.0
    source: self-profile
    direction: above
  - after: "2027-05-08"
---
`
	triggers, err := parseTriggersFromFrontmatter(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 2 {
		t.Fatalf("expected 2 triggers, got %d", len(triggers))
	}
	if triggers[0].Direction != "above" {
		t.Errorf("direction parse: got %q, want above", triggers[0].Direction)
	}
}

func TestValidateTrigger_DirectionUnsupported(t *testing.T) {
	src := `---
review-triggers:
  - metric: foo
    operator: ">"
    threshold: 1
    source: self-profile
    direction: sideways
---
`
	_, err := parseTriggersFromFrontmatter(strings.NewReader(src))
	if err == nil {
		t.Error("expected error for unknown direction")
	}
}
