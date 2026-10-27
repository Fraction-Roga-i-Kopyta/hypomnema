// Package memindex renders a native MEMORY.md index — the table-of-contents
// the harness injects from <project>/memory/MEMORY.md. v2 owns this file: v1
// generated it from the v1 tree with ../../../.claude/memory links, but native
// files are flat siblings of MEMORY.md, so links are bare slugs.
package memindex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// DefaultMaxBytes bounds the rendered index. The harness truncates a native
// MEMORY.md over ~24.4KB and warns; staying under keeps the whole index
// loadable.
const DefaultMaxBytes = 24_000

// descCap bounds each description so one long entry can't dominate the index;
// MEMORY.md is a table of contents, not the content itself.
const descCap = 120

// Render builds the MEMORY.md body for the given native files. Links are
// sibling-relative (bare slug). Output never exceeds maxBytes and always ends
// on a complete line: entries are emitted in the order given (callers pass
// them ranked) and the tail is dropped once the budget is reached.
func Render(files []native.MemFile, maxBytes int) string {
	return render("# Memory Index\n", files, maxBytes, func(f native.MemFile) string { return f.Slug })
}

// RenderGlobal builds the index of the global store. The harness never loads
// that store and it is no project's sibling, so each fact links by absolute
// path: the model can Read it from any project. Written beside the global
// facts; a user who turns off UserPromptSubmit injection (pull-only mode)
// imports it from CLAUDE.md so the global store stays visible.
func RenderGlobal(files []native.MemFile, maxBytes int) string {
	const header = "# Global Memory Index\n" +
		"Facts that apply in every project. Read a file when its description fits the task.\n"
	return render(header, files, maxBytes, func(f native.MemFile) string {
		if f.Path != "" {
			return f.Path
		}
		return f.Slug
	})
}

func render(header string, files []native.MemFile, maxBytes int, link func(native.MemFile) string) string {
	var b strings.Builder
	b.WriteString(header)
	for _, f := range files {
		name := f.Name
		if name == "" {
			name = strings.TrimSuffix(f.Slug, ".md")
		}
		line := fmt.Sprintf("- [%s](%s) — %s\n", name, link(f), truncate(f.Description, descCap))
		if b.Len()+len(line) > maxBytes {
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

// truncate caps s at n runes (not bytes), appending an ellipsis so the cut is
// visible. Rune-safe so multi-byte (Cyrillic, CJK) descriptions never split a
// codepoint.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n]), " ") + "…"
}

// Write atomically replaces <dir>/MEMORY.md with content. Temp-file +
// rename (invariant I3) so a torn write never leaves the harness a
// half-written index to inject.
func Write(dir, content string) error {
	tmp, err := os.CreateTemp(dir, ".MEMORY.*.tmp")
	if err != nil {
		return fmt.Errorf("memindex.Write: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("memindex.Write: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("memindex.Write: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, "MEMORY.md")); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("memindex.Write: rename: %w", err)
	}
	return nil
}
