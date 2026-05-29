package closer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/jsonl"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/profile"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

// Input is the parsed Stop-hook envelope plus resolved paths.
type Input struct {
	SessionID      string
	CWD            string
	TranscriptPath string
	ClaudeHome     string
	MemoryDir      string
	Today          string
}

// Result reports what close did (for diagnostics).
type Result struct {
	Useful int
	Silent int
	Staled int
}

// Run performs the close path: classify injected memories, emit closing WAL
// events, refresh the sidecar, regenerate the self-profile, decay stale rows.
// Best-effort throughout — a failure in one step never aborts the others.
func Run(in Input) (Result, error) {
	var res Result
	injected := readInjectedSet(in.MemoryDir, in.SessionID)
	names := slugNames(in.ClaudeHome, in.CWD)

	var text string
	if sess, err := jsonl.ReadSession(in.TranscriptPath); err == nil {
		text = sess.Text
	}

	useful, silent := Classify(injected, names, text)
	res.Useful, res.Silent = len(useful), len(silent)
	sid := wal.SanitizeField(in.SessionID)
	for _, slug := range useful {
		appendWAL(in.MemoryDir, in.Today, "trigger-useful", slug, sid)
	}
	for _, slug := range silent {
		appendWAL(in.MemoryDir, in.Today, "trigger-silent", slug, sid)
	}
	metrics := fmt.Sprintf("%s|session-metrics|domains:_global_,error_count:0,tool_calls:0,duration:0s|%s", in.Today, sid)
	wal.Append(in.MemoryDir, metrics, "")
	wal.Append(in.MemoryDir, fmt.Sprintf("%s|session-close|%s|%s", in.Today, sid, sid), "")

	if s, err := sidecar.Open(filepath.Join(in.MemoryDir, ".sidecar.db")); err == nil {
		files := collectNative(in.ClaudeHome, in.CWD)
		_ = sidecar.Reproject(s, files, filepath.Join(in.MemoryDir, ".wal"))
		if n, derr := s.MarkStale(in.Today); derr == nil {
			res.Staled = n
		}
		s.Close()
	}
	_ = profile.Generate(in.MemoryDir)

	return res, nil
}

func readInjectedSet(memDir, sessionID string) []string {
	b, err := os.ReadFile(filepath.Join(memDir, ".runtime", "injected-"+sessionID+".list"))
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

func collectNative(claudeHome, cwd string) []native.MemFile {
	osHome := filepath.Dir(claudeHome)
	proj, _ := native.List(native.ProjectMemoryDir(osHome, cwd))
	glob, _ := native.List(native.GlobalMemoryDir(osHome))
	return append(proj, glob...)
}

func slugNames(claudeHome, cwd string) map[string]string {
	out := map[string]string{}
	for _, f := range collectNative(claudeHome, cwd) {
		out[f.Slug] = f.Name
	}
	return out
}

func appendWAL(memDir, day, event, slug, sid string) {
	line := fmt.Sprintf("%s|%s|%s|%s", day, event, wal.SanitizeField(slug), sid)
	wal.Append(memDir, line, "")
}
