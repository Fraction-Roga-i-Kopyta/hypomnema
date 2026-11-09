package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/memindex"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/pathutil"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

// runRetire: memoryctl retire <slug> [--reason "..."] [--superseded-by <ref>] [--project TAG]
// Moves the fact into its store's .archive/ subdirectory, stamps tombstone
// frontmatter (retired/retire-reason/superseded-by/archived-from), and emits
// a `retire` WAL event. Interactive verb: errors visible, exit 1 failure / 2
// misuse.
func runRetire(args []string) {
	var slug, reason, successor, project string
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
		case args[i] == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "memoryctl retire: --project needs a value")
				os.Exit(2)
			}
			project = args[i]
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
		fmt.Fprintln(os.Stderr, "memoryctl retire: missing slug\nUsage: memoryctl retire <slug> [--reason ...] [--superseded-by ...] [--project TAG]")
		os.Exit(2)
	}
	doRetire(slug, reason, successor, project)
	fmt.Printf("retired %s\n", slug)
}

// runRevive: memoryctl revive <slug> [--project TAG] — undoes a retire: moves
// the file back out of .archive/, strips tombstone keys, emits a `revive` WAL
// event.
func runRevive(args []string) {
	var slug, project string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-h" || args[i] == "--help":
			fmt.Print(usage)
			return
		case args[i] == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "memoryctl revive: --project needs a value")
				os.Exit(2)
			}
			project = args[i]
		case strings.HasPrefix(args[i], "-"), slug != "":
			fmt.Fprintln(os.Stderr, "Usage: memoryctl revive <slug> [--project TAG]")
			os.Exit(2)
		default:
			slug = args[i]
		}
	}
	if slug == "" {
		fmt.Fprintln(os.Stderr, "Usage: memoryctl revive <slug> [--project TAG]")
		os.Exit(2)
	}
	doRevive(slug, project)
	fmt.Printf("revived %s\n", slug)
}

func doRetire(slug, reason, successor, project string) {
	f, st := resolveFactIn(slug, project)
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		fatalf("retire: read %s: %v", f.Path, err)
	}
	stamped := stampTombstone(string(raw), today(), reason, successor, f.Slug)

	dir := filepath.Dir(f.Path)
	archDir := filepath.Join(dir, ".archive")
	if err := os.MkdirAll(archDir, 0o755); err != nil {
		fatalf("retire: mkdir %s: %v", archDir, err)
	}
	dst := archivePath(archDir, filepath.Base(f.Path))
	if err := pathutil.WriteFileAtomic(dst, []byte(stamped), 0o644); err != nil {
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
		_ = pathutil.WriteFileAtomic(f.Path, raw, 0o644)
		_ = os.Remove(dst)
		fatalf("retire: wal append: %v (fact left untouched)", err)
	}
	refreshStoreIndex(st)
	refreshSidecar(st)
}

func doRevive(slug, project string) {
	want := strings.TrimSuffix(slug, ".md") + ".md"
	var stores []native.Store
	if project == "" {
		r := resolveStore("")
		stores = []native.Store{
			{Dir: r.Dir, Project: r.Project},
			{Dir: native.GlobalMemoryDir(filepath.Dir(claudeDir())), Project: native.GlobalProject},
		}
	} else {
		stores = []native.Store{storeByTag(project)}
	}
	for _, st := range stores {
		src := newestArchived(filepath.Join(st.Dir, ".archive"), want)
		if src == "" {
			continue
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		dst := filepath.Join(st.Dir, want)
		if _, err := os.Stat(dst); err == nil {
			fatalf("revive: %s already exists in the live store", dst)
		}
		if err := pathutil.WriteFileAtomic(dst, []byte(stripTombstone(string(raw))), 0o644); err != nil {
			fatalf("revive: write %s: %v", dst, err)
		}
		_ = os.Remove(src)
		// A failed revive event is self-healing (file presence wins at the
		// next reproject: the upsert loop writes frontmatter status), so a
		// WAL error here is reported but the restore is not rolled back.
		if err := emitLifecycleEvent("revive", native.QKey(st.Project, strings.TrimSuffix(want, ".md"))); err != nil {
			fmt.Fprintf(os.Stderr, "memoryctl revive: wal append: %v (file restored; sidecar heals at next close)\n", err)
			refreshStoreIndex(st)
			return
		}
		refreshStoreIndex(st)
		refreshSidecar(st)
		return
	}
	fatalf("revive: %s not found in any .archive/", want)
}

// tombstoneField returns the column-0 frontmatter value for key ("" if absent).
func tombstoneField(content, key string) string {
	lines := strings.Split(content, "\n")
	if strings.TrimRight(lines[0], " \t\r") != "---" {
		return ""
	}
	for _, ln := range lines[1:] {
		if strings.TrimRight(ln, " \t\r") == "---" {
			break
		}
		if strings.HasPrefix(ln, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(ln, key+":"))
		}
	}
	return ""
}

// newestArchived picks the archived file to revive for want ("<slug>.md"):
// the newest (by `retired:` date, then mtime) whose archived-from names the
// slug. retire writes <stem>-<n>.md on a collision, and another fact may
// legitimately be called wave-4.md, so the file name alone proves nothing.
// A tombstone written before archived-from existed falls back to exactly
// <want>. Returns "" when nothing matches.
func newestArchived(archDir, want string) string {
	stem := strings.TrimSuffix(want, ".md")
	entries, err := os.ReadDir(archDir)
	if err != nil {
		return ""
	}
	best, bestRetired := "", ""
	var bestMod int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p := filepath.Join(archDir, e.Name())
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		from := tombstoneField(string(raw), "archived-from")
		if from == "" && e.Name() == want {
			from = stem // legacy tombstone
		}
		if strings.TrimSuffix(from, ".md") != stem {
			continue
		}
		retired := tombstoneField(string(raw), "retired")
		var mod int64
		if fi, err := e.Info(); err == nil {
			mod = fi.ModTime().UnixNano()
		}
		if best == "" || retired > bestRetired || (retired == bestRetired && mod > bestMod) {
			best, bestRetired, bestMod = p, retired, mod
		}
	}
	return best
}

// storeByTag finds the store whose Project tag equals tag (exit 1 if none).
func storeByTag(tag string) native.Store {
	for _, s := range native.AllStores(configDir(), claudeDir(), resolveStore("").Store) {
		if s.Project == tag {
			return s
		}
	}
	fmt.Fprintf(os.Stderr, "memoryctl: no store %q\n", tag)
	os.Exit(1)
	return native.Store{}
}

// resolveFactIn finds a live fact by basename. project == "" searches the
// current store plus global (ambiguity is fatal, exit 2 — the operator must
// name the copy with --project); otherwise only the store with that Project
// tag is searched (unknown tag: exit 1).
func resolveFactIn(slug, project string) (native.MemFile, native.Store) {
	want := strings.TrimSuffix(slug, ".md") + ".md"
	var stores []native.Store
	if project == "" {
		r := resolveStore("")
		stores = []native.Store{
			{Dir: r.Dir, Project: r.Project},
			{Dir: native.GlobalMemoryDir(filepath.Dir(claudeDir())), Project: native.GlobalProject},
		}
	} else {
		stores = []native.Store{storeByTag(project)}
	}
	type hit struct {
		f native.MemFile
		s native.Store
	}
	var hits []hit
	for _, st := range stores {
		files, _ := native.List(st.Dir)
		for _, f := range files {
			if f.Slug == want {
				f.Project = st.Project
				hits = append(hits, hit{f, st})
			}
		}
	}
	switch len(hits) {
	case 0:
		if project != "" {
			fatalf("no fact named %q in store %q", want, project)
		}
		fatalf("no fact named %q in the project or global store", want)
	case 1:
		return hits[0].f, hits[0].s
	}
	fmt.Fprintf(os.Stderr, "memoryctl: %q exists in more than one store:\n", want)
	for _, h := range hits {
		fmt.Fprintf(os.Stderr, "  - %s (%s)\n", h.f.Path, h.f.Project)
	}
	fmt.Fprintln(os.Stderr, "name the intended copy with --project <tag> (e.g. --project global)")
	os.Exit(2)
	return native.MemFile{}, native.Store{}
}

// stampTombstone inserts the tombstone keys before the closing frontmatter
// fence; a file without frontmatter gets a minimal block prepended.
// archivedFrom records the live slug so revive can find the file again under
// a collision-renamed archive name.
func stampTombstone(content, day, reason, successor, archivedFrom string) string {
	var keys strings.Builder
	fmt.Fprintf(&keys, "retired: %s\n", day)
	if reason != "" {
		fmt.Fprintf(&keys, "retire-reason: %s\n", reason)
	}
	if successor != "" {
		fmt.Fprintf(&keys, "superseded-by: %s\n", successor)
	}
	if archivedFrom != "" {
		fmt.Fprintf(&keys, "archived-from: %s\n", strings.TrimSuffix(archivedFrom, ".md"))
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

// stripTombstone removes the tombstone keys from the frontmatter
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
			if k == "retired" || k == "retire-reason" || k == "superseded-by" || k == "archived-from" {
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

// refreshStoreIndex regenerates the MEMORY.md of the store a fact lived in:
// the global store through its guarded writer (never over a foreign index),
// any project store through the plain one.
func refreshStoreIndex(st native.Store) {
	files, err := native.List(st.Dir)
	if err != nil {
		return
	}
	if st.Project == native.GlobalProject {
		_ = memindex.WriteGlobal(st.Dir, memindex.RenderGlobal(files, memindex.DefaultMaxBytes))
		return
	}
	_ = memindex.Write(st.Dir, memindex.Render(files, memindex.DefaultMaxBytes))
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "memoryctl "+format+"\n", a...)
	os.Exit(1)
}
