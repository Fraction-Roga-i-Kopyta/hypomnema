package decisions

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/pathutil"
)

// ReadADR reads an ADR markdown file and returns the `review-triggers:`
// block from its frontmatter, plus the slug (basename without .md).
// Missing frontmatter or an absent `review-triggers:` key are not
// errors — the caller just gets an empty slice and can treat the ADR
// as having no structured triggers (prose-only, which is fine).
//
// Parse errors inside `review-triggers:` (unknown operator, bad date)
// bubble up as a typed error so the caller can surface the ADR as
// malformed without skipping it silently.
func ReadADR(path string) (slug string, triggers []Trigger, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	slug = slugFromPath(path)
	triggers, err = parseTriggersFromFrontmatter(f)
	if err != nil {
		return slug, nil, fmt.Errorf("%s: %w", path, err)
	}
	return slug, triggers, nil
}

// slugFromPath is a local thin wrapper around pathutil.SlugFromPath.
// Kept as a file-private helper so calls inside this package read as
// domain code rather than a cross-package import at every site.
func slugFromPath(path string) string {
	return pathutil.SlugFromPath(path)
}

// parseTriggersFromFrontmatter streams the file line-by-line, skips
// past the opening `---`, collects the block under `review-triggers:`,
// and parses each list item into a Trigger. Stops on the closing
// `---`.
//
// The input grammar is a strict subset of YAML — we only accept the
// two documented shapes:
//
//   - metric: name
//     operator: "<"
//     threshold: 0.30
//     source: self-profile
//
//   - after: "2026-07-22"
//
// The keys may appear in any order within an item. Quotes are
// stripped, whitespace trimmed. A line that doesn't start with `- `
// or two-space indentation + `key:` ends the block.
func parseTriggersFromFrontmatter(r io.Reader) ([]Trigger, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Walk past the opening `---`; fail silently if there isn't one.
	// (ADRs without frontmatter simply have no triggers.)
	if !scanToFrontmatterOpen(sc) {
		return nil, nil
	}

	var triggers []Trigger
	var current *Trigger
	inBlock := false

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "---" {
			break // closing frontmatter
		}

		if !inBlock {
			if strings.HasPrefix(line, "review-triggers:") {
				inBlock = true
			}
			continue
		}

		// We're inside the review-triggers: block. Three line shapes:
		//   (a) "  - key: value"      — start of a new item
		//   (b) "    key: value"      — continuation of current item
		//   (c) anything else         — block ends
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			// Left the block (a top-level key at column 0 or no indent).
			// Flush current, stop.
			if current != nil {
				triggers = append(triggers, *current)
				current = nil
			}
			break
		}

		if strings.HasPrefix(trimmed, "- ") {
			// Flush prior item; start new one.
			if current != nil {
				triggers = append(triggers, *current)
			}
			current = &Trigger{}
			// Parse the first key:value on the same line.
			if err := applyKV(current, trimmed[2:]); err != nil {
				return nil, err
			}
			continue
		}

		// Continuation of current item.
		if current == nil {
			// Malformed: continuation without a leading `- `. Skip.
			continue
		}
		if err := applyKV(current, trimmed); err != nil {
			return nil, err
		}
	}
	if current != nil {
		triggers = append(triggers, *current)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	// Validate each trigger's shape.
	for i, t := range triggers {
		if err := validateTrigger(t); err != nil {
			return nil, fmt.Errorf("trigger #%d: %w", i+1, err)
		}
	}
	return triggers, nil
}

func scanToFrontmatterOpen(sc *bufio.Scanner) bool {
	for sc.Scan() {
		if strings.TrimRight(sc.Text(), "\r") == "---" {
			return true
		}
	}
	return false
}

// applyKV splits "key: value", strips optional quotes, and populates
// the corresponding field of the Trigger. Unknown keys are ignored
// (forward-compat per docs/FORMAT.md §1 "Ignore unknown frontmatter
// fields").
func applyKV(t *Trigger, kv string) error {
	idx := strings.IndexByte(kv, ':')
	if idx < 0 {
		return nil
	}
	key := strings.TrimSpace(kv[:idx])
	val := strings.TrimSpace(kv[idx+1:])
	val = strings.TrimSpace(val)
	// Strip surrounding quotes (YAML allows "x" or 'x').
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') ||
			(val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
	}
	// Strip trailing comment: `# ...` AFTER the unquoted value.
	// (Quoted values already had their comments stripped with the
	// quote pair; unquoted ones need manual handling.)
	if strings.HasPrefix(kv, key) {
		if h := strings.Index(val, " #"); h >= 0 {
			val = strings.TrimSpace(val[:h])
		}
	}

	switch key {
	case "metric":
		t.Metric = val
	case "operator":
		t.Operator = val
	case "threshold":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fmt.Errorf("threshold %q is not a number", val)
		}
		t.Threshold = f
	case "source":
		t.Source = val
	case "after":
		t.After = val
	case "direction":
		t.Direction = val
	}
	return nil
}

// validateTrigger enforces that a parsed Trigger matches one of the
// two documented shapes and that its fields are individually valid.
func validateTrigger(t Trigger) error {
	if t.After != "" {
		if _, err := time.Parse("2006-01-02", t.After); err != nil {
			return fmt.Errorf("after %q is not YYYY-MM-DD", t.After)
		}
		if t.Metric != "" || t.Operator != "" || t.Source != "" {
			return fmt.Errorf("calendar trigger must not set metric/operator/source")
		}
		return nil
	}
	if t.Metric == "" {
		return fmt.Errorf("missing metric")
	}
	switch t.Operator {
	case "<", "<=", ">", ">=", "==", "!=":
	default:
		return fmt.Errorf("operator %q unsupported (want <, <=, >, >=, ==, !=)", t.Operator)
	}
	switch t.Source {
	case "self-profile", "wal", "file":
	case "":
		return fmt.Errorf("missing source (want self-profile, wal, or file)")
	default:
		return fmt.Errorf("source %q unsupported (want self-profile, wal, or file)", t.Source)
	}
	switch t.Direction {
	case "", "above", "below":
	default:
		return fmt.Errorf("direction %q unsupported (want above, below, or unset)", t.Direction)
	}
	return nil
}

// parseISODate returns Unix seconds for a YYYY-MM-DD string. Helper
// used by daysBetween in eval.go; lives here so eval.go stays stdlib-
// time-only in imports.
func parseISODate(s string) (int, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return 0, err
	}
	return int(t.Unix()), nil
}
