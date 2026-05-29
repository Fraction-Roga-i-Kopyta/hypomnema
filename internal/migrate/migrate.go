// Package migrate converts the v1 hypomnema store (8-type rich-frontmatter
// files under ~/.claude/memory/<type>/) into native memory files + a rebuilt
// sidecar. Planning is pure; execution writes files and backs up the old store.
package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// Opts configures a migration plan.
type Opts struct {
	V1Dir     string            // ~/.claude/memory
	GlobalDir string            // ~/.claude/memory-global
	OSHome    string            // ~ (for native ProjectMemoryDir)
	Projects  map[string]string // abs-cwd → project-name (projects.json)
	Today     string            // YYYY-MM-DD
}

// FileConv is the planned disposition of one v1 file.
type FileConv struct {
	OldPath   string
	Slug      string
	Action    string // "keep" | "prune"
	Reason    string
	TargetDir string // native dest dir ("" if prune)
	Type      string // fine type (verbatim from v1)
	Project   string
	Native    string // converted native file content (if keep)
}

// Plan is the full migration plan.
type Plan struct {
	Convs  []FileConv
	Kept   int
	Pruned int
}

var v1Subdirs = []string{"mistakes", "strategies", "feedback", "knowledge", "decisions", "notes", "projects", "continuity"}

// BuildPlan walks the v1 store and decides each file's disposition. Pure
// (reads files, computes; writes nothing).
func BuildPlan(o Opts) (Plan, error) {
	t0 := parseDay(o.Today)
	// name→cwd reverse map for project routing.
	nameToCWD := map[string]string{}
	for cwd, name := range o.Projects {
		nameToCWD[name] = cwd
	}
	var p Plan
	for _, sub := range v1Subdirs {
		dir := filepath.Join(o.V1Dir, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // missing subdir is fine
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			fm, body := splitV1(string(raw))
			conv := FileConv{
				OldPath: path, Slug: e.Name(),
				Type: orDefault(fm["type"], sub), Project: orDefault(fm["project"], "global"),
			}
			if shouldPrune(fm, t0, parseDay(o.Today)) {
				conv.Action = "prune"
				conv.Reason = "ref_count=0, not pinned, >30d"
				p.Pruned++
			} else {
				conv.Action = "keep"
				conv.TargetDir = routeDir(conv.Project, o, nameToCWD)
				conv.Native = toNative(conv.Slug, conv.Type, fm, body)
				p.Kept++
			}
			p.Convs = append(p.Convs, conv)
		}
	}
	return p, nil
}

func shouldPrune(fm map[string]string, today, _ day) bool {
	if fm["status"] == "pinned" {
		return false
	}
	rc, _ := strconv.Atoi(strings.TrimSpace(fm["ref_count"]))
	if rc != 0 {
		return false
	}
	c := parseDayStr(fm["created"])
	if c.zero {
		return false // unknown age → keep, don't prune blindly
	}
	return today.sub(c) > 30
}

func routeDir(project string, o Opts, nameToCWD map[string]string) string {
	if project == "" || project == "global" {
		return o.GlobalDir
	}
	if cwd, ok := nameToCWD[project]; ok {
		return native.ProjectMemoryDir(o.OSHome, cwd)
	}
	return o.GlobalDir // unknown project → global (preserve, don't lose)
}

// toNative composes the native file: minimal frontmatter (name/description/
// type=fine) + a body that folds in mistake root-cause/prevention prose so
// no knowledge is lost.
func toNative(slug, fineType string, fm map[string]string, body string) string {
	name := fm["name"]
	if name == "" {
		name = humanize(strings.TrimSuffix(slug, ".md"))
	}
	desc := fm["description"]
	if desc == "" {
		desc = firstLine(body)
	}
	var nb strings.Builder
	if rc := fm["root-cause"]; rc != "" {
		fmt.Fprintf(&nb, "**Root cause:** %s\n\n", rc)
	}
	if pv := fm["prevention"]; pv != "" {
		fmt.Fprintf(&nb, "**Prevention:** %s\n\n", pv)
	}
	nb.WriteString(body)
	return fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: %s\n---\n%s\n",
		name, desc, fineType, strings.TrimSpace(nb.String()))
}

func humanize(slug string) string {
	s := strings.ReplaceAll(slug, "-", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func firstLine(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimSpace(strings.TrimLeft(ln, "#"))
		ln = strings.TrimSpace(ln)
		if ln != "" {
			if len(ln) > 100 {
				return ln[:100]
			}
			return ln
		}
	}
	return ""
}

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return strings.TrimSpace(v)
}

// splitV1 parses v1 frontmatter into a flat key→value map (block arrays like
// domains/keywords/triggers are skipped — migration doesn't need them; the
// sidecar re-derives keywords). Tolerates quoted scalars + BOM/CRLF.
func splitV1(s string) (map[string]string, string) {
	fm := map[string]string{}
	s = strings.TrimPrefix(s, "\xef\xbb\xbf")
	if !strings.HasPrefix(s, "---") {
		return fm, strings.TrimSpace(s)
	}
	lines := strings.Split(s, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r") == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return fm, strings.TrimSpace(s)
	}
	for _, ln := range lines[1:end] {
		ln = strings.TrimRight(ln, "\r")
		if strings.HasPrefix(ln, "  ") || strings.HasPrefix(ln, "\t") || strings.HasPrefix(strings.TrimSpace(ln), "- ") {
			continue // block-array item — skip
		}
		idx := strings.IndexByte(ln, ':')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(ln[:idx])
		val := strings.Trim(strings.TrimSpace(ln[idx+1:]), `"'`)
		fm[key] = val
	}
	return fm, strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
}

// minimal date helper (avoids importing time across the package surface)
type day struct {
	y, m, d int
	zero    bool
}

func parseDay(s string) day    { return parseDayStr(s) }
func parseDayStr(s string) day {
	var y, m, d int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d-%d-%d", &y, &m, &d); err != nil {
		return day{zero: true}
	}
	return day{y: y, m: m, d: d}
}

// sub returns approximate day difference (good enough for a 30-day prune gate).
func (a day) sub(b day) int {
	if a.zero || b.zero {
		return 0
	}
	return (a.y-b.y)*365 + (a.m-b.m)*30 + (a.d - b.d)
}
