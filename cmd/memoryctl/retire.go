package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/memindex"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

// runRetire: memoryctl retire <slug> [--reason "..."] [--superseded-by <ref>]
// Moves the fact into its store's .archive/ subdirectory, stamps tombstone
// frontmatter (retired/retire-reason/superseded-by), and emits a `retire`
// WAL event. Interactive verb: errors visible, exit 1 failure / 2 misuse.
func runRetire(args []string) {
	var slug, reason, successor string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--reason":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "memoryctl retire: --reason needs a value")
				os.Exit(2)
			}
			reason = args[i]
		case args[i] == "--superseded-by":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "memoryctl retire: --superseded-by needs a value")
				os.Exit(2)
			}
			successor = args[i]
		case args[i] == "-h" || args[i] == "--help":
			fmt.Print(usage)
			return
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(os.Stderr, "memoryctl retire: unknown flag %q\n", args[i])
			os.Exit(2)
		default:
			if slug != "" {
				fmt.Fprintln(os.Stderr, "memoryctl retire: exactly one slug expected")
				os.Exit(2)
			}
			slug = args[i]
		}
	}
	if slug == "" {
		fmt.Fprintln(os.Stderr, "memoryctl retire: missing slug\nUsage: memoryctl retire <slug> [--reason ...] [--superseded-by ...]")
		os.Exit(2)
	}
	doRetire(slug, reason, successor)
	fmt.Printf("retired %s\n", slug)
}

// runRevive: memoryctl revive <slug> — undoes a retire: moves the file back
// out of .archive/, strips tombstone keys, emits a `revive` WAL event.
func runRevive(args []string) {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Print(usage)
		return
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "Usage: memoryctl revive <slug>")
		os.Exit(2)
	}
	doRevive(args[0])
	fmt.Printf("revived %s\n", args[0])
}

func doRetire(slug, reason, successor string) {
	f := resolveFact(slug)
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		fatalf("retire: read %s: %v", f.Path, err)
	}
	stamped := stampTombstone(string(raw), today(), reason, successor)

	dir := filepath.Dir(f.Path)
	archDir := filepath.Join(dir, ".archive")
	if err := os.MkdirAll(archDir, 0o755); err != nil {
		fatalf("retire: mkdir %s: %v", archDir, err)
	}
	dst := archivePath(archDir, filepath.Base(f.Path))
	if err := os.WriteFile(dst, []byte(stamped), 0o644); err != nil {
		fatalf("retire: write %s: %v", dst, err)
	}
	if err := os.Remove(f.Path); err != nil {
		_ = os.Remove(dst) // roll back the copy; leave the store intact
		fatalf("retire: remove %s: %v", f.Path, err)
	}

	target := native.QKey(f.Project, strings.TrimSuffix(f.Slug, ".md"))
	if successor != "" {
		target += ">" + strings.Join(strings.Fields(successor), "_")
	}
	if reason != "" {
		target += ":" + strings.Join(strings.Fields(reason), "_")
	}
	if err := emitLifecycleEvent("retire", target); err != nil {
		// All-or-nothing: an archived file WITHOUT a retire event would
		// reconcile as 'deleted' (not 'retired') and block a repeat retire —
		// restore the live file so the verb can simply be re-run.
		_ = os.WriteFile(f.Path, raw, 0o644)
		_ = os.Remove(dst)
		fatalf("retire: wal append: %v (fact left untouched)", err)
	}
	refreshProjectIndex(dir)
}

func doRevive(slug string) {
	want := strings.TrimSuffix(slug, ".md") + ".md"
	home := filepath.Dir(claudeDir())
	stores := []string{
		native.ProjectMemoryDir(home, projectCWD()),
		native.GlobalMemoryDir(home),
	}
	for _, store := range stores {
		src := filepath.Join(store, ".archive", want)
		raw, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		dst := filepath.Join(store, want)
		if _, err := os.Stat(dst); err == nil {
			fatalf("revive: %s already exists in the live store", dst)
		}
		if err := os.WriteFile(dst, []byte(stripTombstone(string(raw))), 0o644); err != nil {
			fatalf("revive: write %s: %v", dst, err)
		}
		_ = os.Remove(src)
		project := native.SlugFromCWD(projectCWD())
		if store == native.GlobalMemoryDir(home) {
			project = native.GlobalProject
		}
		// A failed revive event is self-healing (file presence wins at the
		// next reproject: the upsert loop writes frontmatter status), so a
		// WAL error here is reported but the restore is not rolled back.
		if err := emitLifecycleEvent("revive", native.QKey(project, strings.TrimSuffix(want, ".md"))); err != nil {
			fmt.Fprintf(os.Stderr, "memoryctl revive: wal append: %v (file restored; sidecar heals at next close)\n", err)
		}
		refreshProjectIndex(store)
		return
	}
	fatalf("revive: %s not found in any .archive/", want)
}

// resolveFact finds the single live native file whose basename matches slug.
// Ambiguity across stores is fatal (the operator must name the copy).
func resolveFact(slug string) native.MemFile {
	want := strings.TrimSuffix(slug, ".md") + ".md"
	files, err := collectNative()
	if err != nil {
		fatalf("retire: %v", err)
	}
	var hits []native.MemFile
	for _, f := range files {
		if f.Slug == want {
			hits = append(hits, f)
		}
	}
	switch len(hits) {
	case 0:
		fatalf("no fact named %q in the project or global store", want)
	case 1:
		return hits[0]
	}
	fmt.Fprintf(os.Stderr, "memoryctl: %q exists in more than one store:\n", want)
	for _, f := range hits {
		fmt.Fprintf(os.Stderr, "  - %s (%s)\n", f.Path, f.Project)
	}
	fmt.Fprintln(os.Stderr, "move or rename one copy, or run with CLAUDE_PROJECT_CWD pointed at the intended store")
	os.Exit(2)
	return native.MemFile{}
}

// stampTombstone inserts the tombstone keys before the closing frontmatter
// fence; a file without frontmatter gets a minimal block prepended.
func stampTombstone(content, day, reason, successor string) string {
	var keys strings.Builder
	fmt.Fprintf(&keys, "retired: %s\n", day)
	if reason != "" {
		fmt.Fprintf(&keys, "retire-reason: %s\n", reason)
	}
	if successor != "" {
		fmt.Fprintf(&keys, "superseded-by: %s\n", successor)
	}
	lines := strings.Split(content, "\n")
	if strings.TrimRight(lines[0], " \t\r") == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimRight(lines[i], " \t\r") == "---" {
				return strings.Join(lines[:i], "\n") + "\n" + keys.String() + strings.Join(lines[i:], "\n")
			}
		}
	}
	return "---\n" + keys.String() + "---\n" + content
}

// stripTombstone removes the three tombstone keys from the frontmatter
// block. Only column-0 keys count — an indented line is block-scalar
// continuation content, never a top-level key (mirrors splitFrontmatter's
// G2 rule), so "  superseded-by: x" inside a `root-cause: |` body survives.
func stripTombstone(content string) string {
	lines := strings.Split(content, "\n")
	if strings.TrimRight(lines[0], " \t\r") != "---" {
		return content
	}
	out := lines[:0:0]
	inFM := true
	for i, ln := range lines {
		if i > 0 && inFM && strings.TrimRight(ln, " \t\r") == "---" {
			inFM = false
		}
		if i > 0 && inFM && len(ln) > 0 && ln[0] != ' ' && ln[0] != '\t' {
			k := strings.TrimSpace(strings.SplitN(ln, ":", 2)[0])
			if k == "retired" || k == "retire-reason" || k == "superseded-by" {
				continue
			}
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// archivePath returns a collision-free destination inside archDir.
func archivePath(archDir, base string) string {
	dst := filepath.Join(archDir, base)
	stem := strings.TrimSuffix(base, ".md")
	for n := 2; ; n++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			return dst
		}
		dst = filepath.Join(archDir, fmt.Sprintf("%s-%d.md", stem, n))
	}
}

// emitLifecycleEvent appends one lifecycle WAL row with the CLI's session
// identity (HYPOMNEMA_SESSION_ID → CLAUDE_CODE_SESSION_ID → "cli"). The
// error is returned so callers can roll back file moves (all-or-nothing).
func emitLifecycleEvent(event, target string) error {
	sid := os.Getenv("HYPOMNEMA_SESSION_ID")
	if sid == "" {
		sid = os.Getenv("CLAUDE_CODE_SESSION_ID")
	}
	if sid == "" {
		sid = "cli"
	}
	line := fmt.Sprintf("%s|%s|%s|%s", today(), event,
		wal.SanitizeField(target), wal.SanitizeField(sid))
	return wal.AppendStrict(memoryDir(), line, "")
}

// refreshProjectIndex regenerates MEMORY.md when dir is the project store
// (the global store carries no index — MEMORY.md is project-local).
func refreshProjectIndex(dir string) {
	home := filepath.Dir(claudeDir())
	if dir != native.ProjectMemoryDir(home, projectCWD()) {
		return
	}
	if files, err := native.List(dir); err == nil {
		_ = memindex.Write(dir, memindex.Render(files, memindex.DefaultMaxBytes))
	}
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "memoryctl "+format+"\n", a...)
	os.Exit(1)
}
