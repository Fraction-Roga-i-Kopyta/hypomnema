package main

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/evidence"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/jsonl"
)

// runEvidence dispatches `memoryctl evidence <subcommand>`. First
// subcommand: `learn`. Mirrors the `decisions` / `fts` pattern.
func runEvidence(args []string) {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch args[0] {
	case "learn":
		runEvidenceLearn(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "memoryctl: unknown evidence subcommand %q\n", args[0])
		os.Exit(2)
	}
}

// runEvidenceLearn implements the plan's interactive flow:
//
//   1. Pick the target slug (--target required until we ship
//      --all-pinned; the plan's proposal).
//   2. Scan the last N session JSONLs for trigger-silent events
//      against this target. Sessions where the slug was useful or
//      not injected at all become the noise baseline.
//   3. Run the miner. Display ranked candidates. In dry-run mode,
//      stop here.
//   4. Otherwise, prompt the user for each candidate (accept /
//      reject / reject-pattern). Persist rejections; append
//      accepted phrases to the memory file.
func runEvidenceLearn(args []string) {
	target := ""
	sessions := 30
	dryRun := false
	autoAccept := false // --yes for non-interactive batch
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--target":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--target requires a slug")
				os.Exit(2)
			}
			target = args[i+1]
			i++
		case "--sessions":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--sessions requires a number")
				os.Exit(2)
			}
			var n int
			if _, err := fmt.Sscanf(args[i+1], "%d", &n); err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "--sessions: expected positive integer, got %q\n", args[i+1])
				os.Exit(2)
			}
			sessions = n
			i++
		case "--dry-run":
			dryRun = true
		case "--yes":
			autoAccept = true
		case "-h", "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl evidence learn: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	if target == "" {
		fmt.Fprintln(os.Stderr, "memoryctl evidence learn: --target <slug> is required")
		os.Exit(2)
	}

	memDir := memoryDir()
	targetPath := locateMemoryFile(memDir, target)
	if targetPath == "" {
		fmt.Fprintf(os.Stderr, "memoryctl evidence learn: target %q not found in memory tree\n", target)
		os.Exit(2)
	}

	transcriptPaths, err := recentTranscripts(sessions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl evidence learn: transcripts: %v\n", err)
		os.Exit(2)
	}
	if len(transcriptPaths) == 0 {
		fmt.Fprintln(os.Stderr, "memoryctl evidence learn: no session transcripts under ~/.claude/projects/")
		os.Exit(1)
	}

	silentSessionIDs, err := silentSessionsForTarget(filepath.Join(memDir, ".wal"), target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl evidence learn: wal: %v\n", err)
		os.Exit(2)
	}

	var silentText, noiseText []string
	for _, p := range transcriptPaths {
		s, err := jsonl.ReadSession(p)
		if err != nil || s.Text == "" {
			continue
		}
		if _, isSilent := silentSessionIDs[s.ID]; isSilent {
			silentText = append(silentText, s.Text)
		} else {
			noiseText = append(noiseText, s.Text)
		}
	}

	fmt.Printf("Scanning %d sessions (%d silent, %d noise baseline)\n",
		len(silentText)+len(noiseText), len(silentText), len(noiseText))
	fmt.Printf("Target: %s\n", target)

	existing, err := readEvidenceBlock(targetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl evidence learn: %v\n", err)
		os.Exit(2)
	}
	rejections, err := evidence.LoadRejections(evidence.RejectionsPath(memDir, target))
	if err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl evidence learn: rejections: %v\n", err)
		os.Exit(2)
	}

	// Pre-filter existing + previously-rejected phrases before
	// mining so they don't occupy the top-N slots.
	excluded := append([]string{}, existing...)
	for r := range rejections {
		excluded = append(excluded, r)
	}

	candidates := evidence.Mine(silentText, noiseText, excluded, evidence.Config{})
	if len(candidates) == 0 {
		fmt.Println("No new candidates (all proposals already present or rejected).")
		return
	}

	fmt.Printf("\nCandidates (ranked by silent-session coverage):\n")
	for _, c := range candidates {
		fmt.Printf("  [%d silent / %d noise]  %q\n",
			c.SilentSessions, c.NoiseSessions, c.Phrase)
	}

	if dryRun {
		fmt.Println("\n--dry-run: no changes written.")
		return
	}

	accepted := []string{}
	rejectedPath := evidence.RejectionsPath(memDir, target)
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("\nFor each phrase: [y]es (accept) / [n]o (reject once) / [p]attern (reject forever) / [q]uit")
	for _, c := range candidates {
		ans := "y"
		if !autoAccept {
			fmt.Printf("  %q — (y/n/p/q)? ", c.Phrase)
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				fmt.Fprintf(os.Stderr, "read: %v\n", err)
				break
			}
			ans = strings.ToLower(strings.TrimSpace(line))
			if ans == "" {
				ans = "n"
			}
		}
		switch ans[0] {
		case 'y':
			accepted = append(accepted, c.Phrase)
		case 'p':
			if err := evidence.AppendRejection(rejectedPath, c.Phrase); err != nil {
				fmt.Fprintf(os.Stderr, "append rejection: %v\n", err)
			}
		case 'q':
			goto end
		default:
			// 'n' (or anything else) — skip for this run, no persist.
		}
	}
end:
	if len(accepted) == 0 {
		fmt.Println("No phrases accepted.")
		return
	}
	if err := evidence.AppendToFrontmatter(targetPath, accepted); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Appended %d phrase(s) to %s\n", len(accepted), targetPath)
}

// --- helpers ---

// locateMemoryFile searches the four type-directories where evidence
// is meaningful (mistakes, feedback, knowledge, strategies) for a
// file matching slug.md. Returns empty string if not found.
func locateMemoryFile(memDir, slug string) string {
	for _, sub := range []string{"mistakes", "feedback", "knowledge", "strategies"} {
		p := filepath.Join(memDir, sub, slug+".md")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// recentTranscripts returns the most-recent `limit` .jsonl files
// under $CLAUDE_HOME/projects/*/*.jsonl (defaulting to ~/.claude/projects),
// sorted newest-first by mtime. Goes through claudeDir() rather than
// os.UserHomeDir so test fixtures and parallel installs that set
// CLAUDE_HOME are respected here too.
func recentTranscripts(limit int) ([]string, error) {
	root := filepath.Join(claudeDir(), "projects")
	var matches []struct {
		path string
		mt   int64
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		// Only stat the entries we actually keep — avoids the per-file
		// stat WalkDir used to do for us, gets us back on par with the
		// old Walk call cost-wise.
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		matches = append(matches, struct {
			path string
			mt   int64
		}{p, info.ModTime().Unix()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].mt > matches[j].mt })
	if len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.path
	}
	return out, nil
}

// silentSessionsForTarget reads the WAL and returns the set of
// session_ids where this target was flagged `trigger-silent` or
// `evidence-empty`. Those are the sessions the miner should draw
// phrases from.
func silentSessionsForTarget(walPath, target string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	f, err := os.Open(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "|", 4)
		if len(parts) < 4 {
			continue
		}
		if parts[2] != target {
			continue
		}
		if parts[1] == "trigger-silent" || parts[1] == "evidence-empty" {
			out[parts[3]] = struct{}{}
		}
	}
	return out, sc.Err()
}

// readEvidenceBlock returns the phrases currently in the target
// file's evidence: block (lowercased). Used to pre-filter miner
// proposals.
func readEvidenceBlock(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	openIdx, closeIdx := -1, -1
	for i, l := range lines {
		if l == "---" {
			if openIdx < 0 {
				openIdx = i
			} else if closeIdx < 0 {
				closeIdx = i
				break
			}
		}
	}
	if openIdx < 0 || closeIdx < 0 {
		return nil, nil
	}
	inBlock := false
	var out []string
	for i := openIdx + 1; i < closeIdx; i++ {
		line := lines[i]
		if strings.HasPrefix(line, "evidence:") {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		v, ok := evidence.ParseYAMLListItem(line)
		if !ok {
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "  ") {
				break
			}
			continue
		}
		out = append(out, strings.ToLower(v))
	}
	return out, nil
}
