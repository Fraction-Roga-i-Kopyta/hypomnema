// Package invariants implements automated checks for the rules in
// docs/INVARIANTS.md. Not every invariant is mechanically checkable;
// this package covers the ones that are (grep-based scans, structural
// constraints, file-shape rules). Soft invariants (parity contracts,
// API-design rules) remain human-judgment items and are referenced by
// the catalog itself.
//
// Each checker is a pure function: takes a repo root, returns a slice
// of violations. The CLI wrapper runs checkers in order and reports
// them in a uniform format. Adding a new checker is one function in
// this package + one entry in the All() list.
package invariants

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Violation describes one rule break.
type Violation struct {
	ID       string // "I1", "I3", etc. — matches docs/INVARIANTS.md
	Path     string // file:line or directory where the rule is broken
	Message  string // one-line description
	Severity string // "error" (blocking) | "warn" (advisory)
}

// Checker is the contract every invariant check implements.
type Checker interface {
	ID() string
	Name() string
	Check(repoRoot string) ([]Violation, error)
}

// All returns the checkers in the order they should run. The order
// matters only for output stability, not correctness.
func All() []Checker {
	return []Checker{
		atomicWriteChecker{},
		hookFailSafeChecker{},
	}
}

// --- I3: atomic writes for multi-line files ---

type atomicWriteChecker struct{}

func (atomicWriteChecker) ID() string   { return "I3" }
func (atomicWriteChecker) Name() string { return "atomic writes for multi-line files" }

// approvedWriteFileSites lists Go source paths whose os.WriteFile
// calls have been reviewed and are NOT subject to the I3 atomic-
// write rule. Reasons: single-line content (a slug, a JSON tag),
// test fixture writers, ephemeral runtime state. Add to this list
// only with a one-line justification in the comment.
var approvedWriteFileSites = map[string]string{
	"internal/dedup/dedup.go": "incrementRecurrence: read-modify-write under WAL lock; atomicity covered by lock",
	"internal/profile/decisions_append.go": "AppendDecisionsPressure: atomic-via-rename is implemented inside this function",
	"internal/evidence/evidence.go": "AppendToFrontmatter implements its own tmp+rename; intermediate WriteFile calls cover non-frontmatter siblings",
}

func (c atomicWriteChecker) Check(repoRoot string) ([]Violation, error) {
	internalDir := filepath.Join(repoRoot, "internal")
	var violations []Violation
	err := filepath.WalkDir(internalDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		// Only Go production sources — skip tests.
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Match the actual function call (paren immediately after the
		// identifier) — this avoids false positives on comments,
		// string literals, and identifier references that just mention
		// the function name. Must NOT match `_ = os.WriteFile` discard
		// patterns either (they are still calls); accepted because
		// any production discard of WriteFile is itself a smell.
		if !reWriteFileCall.MatchString(string(body)) {
			return nil
		}
		rel, _ := filepath.Rel(repoRoot, path)
		if reason, ok := approvedWriteFileSites[rel]; ok {
			_ = reason // approved; skip
			return nil
		}
		for i, line := range strings.Split(string(body), "\n") {
			if reWriteFileCall.MatchString(line) {
				violations = append(violations, Violation{
					ID:       c.ID(),
					Path:     fmt.Sprintf("%s:%d", rel, i+1),
					Message:  "os.WriteFile in production code without approval — use os.CreateTemp + os.Rename or add to approvedWriteFileSites with justification",
					Severity: "error",
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return violations, nil
}

// --- I5: hook fail-safe (no `set -e` or `set -u` in production hooks) ---

type hookFailSafeChecker struct{}

func (hookFailSafeChecker) ID() string   { return "I5" }
func (hookFailSafeChecker) Name() string { return "fail-safe on hook errors (set -o pipefail, not -e/-u)" }

// approvedFailFastHooks lists hook scripts that legitimately use
// `set -e` — currently the test runner and one-shot CLI helpers
// where fail-fast is the expected behaviour, not a contract violation.
// Production hooks (called by Claude Code lifecycle events) MUST
// stay out of this list.
var approvedFailFastHooks = map[string]string{
	"hooks/test-memory-hooks.sh":     "test runner: fail-fast is the test-suite contract",
	"hooks/test-fixture-snapshot.sh": "test runner: fail-fast is the test-suite contract",
	"hooks/memory-fts-sync.sh":       "one-shot index sync: idempotent, no Claude Code session impact",
	"hooks/bench-memory.sh":          "one-shot benchmark: fail-fast surfaces measurement bugs",
}

// reWriteFileCall matches the actual call syntax — identifier
// followed by an opening paren. Matching the call form (rather than
// the bare identifier) avoids false positives on comments and
// string literals that just mention the function name.
var reWriteFileCall = regexp.MustCompile(`\bos\.WriteFile\s*\(`)

// reBadSet matches `set -e`, `set -eu`, `set -u`, `set -euo pipefail`,
// `set -ueo pipefail`, `set -euxo pipefail` and the like — anything
// with `-e` or `-u` flags that would break the fail-safe hook
// contract. `set -o pipefail` alone (the explicitly-allowed form)
// does not match because the dash-flag block contains no `e` or `u`.
// Trailing whitespace, end-of-line, or further arguments (`pipefail`)
// are all accepted; we key only on the dash-flag block itself.
var reBadSet = regexp.MustCompile(`(?m)^\s*set\s+-[A-Za-z]*[eu][A-Za-z]*(\s|$)`)

func (c hookFailSafeChecker) Check(repoRoot string) ([]Violation, error) {
	hooksDir := filepath.Join(repoRoot, "hooks")
	var violations []Violation
	err := filepath.WalkDir(hooksDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".sh") {
			return nil
		}
		rel, _ := filepath.Rel(repoRoot, path)
		if reason, ok := approvedFailFastHooks[rel]; ok {
			_ = reason
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Find each occurrence with line number for reporting.
		for i, line := range strings.Split(string(body), "\n") {
			if reBadSet.MatchString(line) {
				violations = append(violations, Violation{
					ID:       c.ID(),
					Path:     fmt.Sprintf("%s:%d", rel, i+1),
					Message:  fmt.Sprintf("production hook uses %q — fail-safe contract requires `set -o pipefail` only (or add to approvedFailFastHooks with justification)", strings.TrimSpace(line)),
					Severity: "error",
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return violations, nil
}
