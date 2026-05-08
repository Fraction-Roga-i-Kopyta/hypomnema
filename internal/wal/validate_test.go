package wal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWAL(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".wal")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValidate_PassesCleanWAL(t *testing.T) {
	p := writeWAL(t, strings.Join([]string{
		"2026-04-01|inject|foo-mistake|sess1",
		"2026-04-01|trigger-useful|foo-mistake|sess1",
		"2026-04-02|outcome-positive|foo-mistake|sess2",
	}, "\n")+"\n")

	r, err := Validate(p)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !r.Clean() {
		t.Errorf("expected clean report, got %d failures: %+v", len(r.Failures), r.Failures)
	}
	if r.Total != 3 || r.Passing != 3 {
		t.Errorf("Total=%d Passing=%d, want 3/3", r.Total, r.Passing)
	}
}

func TestValidate_RejectsBrokenColumnCount(t *testing.T) {
	p := writeWAL(t, strings.Join([]string{
		"2026-04-01|inject|foo|sess1",                    // ok
		"2026-04-01|broken|foo|bar|extra",                // 5 cols
		"2026-04-02|short|three|cols",                    // 4 cols (this is fine actually)
		"2026-04-03|alsobroken|two-cols",                 // 3 cols
	}, "\n")+"\n")
	r, err := Validate(p)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(r.Failures) != 2 {
		t.Fatalf("expected 2 failures, got %d: %+v", len(r.Failures), r.Failures)
	}
	if !strings.Contains(r.Failures[0].Reason, "column count = 5") {
		t.Errorf("failure[0] reason mismatch: %s", r.Failures[0].Reason)
	}
	if !strings.Contains(r.Failures[1].Reason, "column count = 3") {
		t.Errorf("failure[1] reason mismatch: %s", r.Failures[1].Reason)
	}
}

func TestValidate_RejectsBadDate(t *testing.T) {
	p := writeWAL(t, "2026-4-01|inject|foo|sess1\n2026-04-1|inject|foo|sess1\nfoo|inject|foo|sess1\n")
	r, _ := Validate(p)
	if len(r.Failures) != 3 {
		t.Errorf("expected 3 date failures, got %d: %+v", len(r.Failures), r.Failures)
	}
	for i, f := range r.Failures {
		if !strings.Contains(f.Reason, "YYYY-MM-DD") {
			t.Errorf("failure[%d] reason mismatch: %s", i, f.Reason)
		}
	}
}

func TestValidate_RejectsBadEvent(t *testing.T) {
	p := writeWAL(t, strings.Join([]string{
		"2026-04-01|InjectCAPS|foo|sess1",      // capitals
		"2026-04-01|2events|foo|sess1",         // starts with digit
		"2026-04-01|under_score|foo|sess1",     // underscore not allowed
		"2026-04-01|ok-event|foo|sess1",        // ok
	}, "\n")+"\n")
	r, _ := Validate(p)
	if len(r.Failures) != 3 {
		t.Errorf("expected 3 event failures, got %d: %+v", len(r.Failures), r.Failures)
	}
}

func TestValidate_RejectsEmptySession(t *testing.T) {
	p := writeWAL(t, "2026-04-01|inject|foo|\n")
	r, _ := Validate(p)
	if len(r.Failures) != 1 || !strings.Contains(r.Failures[0].Reason, "session is empty") {
		t.Errorf("expected 1 empty-session failure, got %+v", r.Failures)
	}
}

func TestValidate_TolerateV2Header(t *testing.T) {
	// Header on row 1 is OK; header-like row in the middle is not.
	p := writeWAL(t, "# hypomnema-wal v2\n2026-04-01|inject|foo|sess1\n# fake|comment|in|middle\n")
	r, _ := Validate(p)
	if r.Total != 2 {
		t.Errorf("expected 2 data rows (header skipped), got Total=%d", r.Total)
	}
	if len(r.Failures) != 1 {
		t.Errorf("expected 1 failure (mid-file header-like row), got %d: %+v", len(r.Failures), r.Failures)
	}
}

func TestValidate_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := Validate(filepath.Join(dir, "nonexistent.wal"))
	if !errors.Is(err, ErrWALMissing) {
		t.Errorf("expected ErrWALMissing, got %v", err)
	}
}

func TestValidate_EmptyFile(t *testing.T) {
	p := writeWAL(t, "")
	r, err := Validate(p)
	if err != nil {
		t.Fatalf("Validate empty: %v", err)
	}
	if !r.Clean() || r.Total != 0 {
		t.Errorf("expected clean empty report, got Total=%d Failures=%+v", r.Total, r.Failures)
	}
}
