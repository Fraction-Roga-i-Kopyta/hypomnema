package fts

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestSync_ExcludesDerivativeViewsFromIndex verifies that auto-generated
// files at memory root (self-profile.md, _agent_context.md, MEMORY.md)
// do not enter the FTS index — closing the strange-loop hazard where
// shadow recall could surface hypomnema's own derived state as
// injectable knowledge.
func TestSync_ExcludesDerivativeViewsFromIndex(t *testing.T) {
	mem := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mem, "mistakes"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Real memory: should be indexed.
	if err := os.WriteFile(filepath.Join(mem, "mistakes", "real.md"),
		[]byte("---\ntype: mistake\n---\nReal body content."), 0o644); err != nil {
		t.Fatal(err)
	}
	// Derivative views at root: must be excluded.
	for _, name := range []string{"self-profile.md", "_agent_context.md", "MEMORY.md"} {
		if err := os.WriteFile(filepath.Join(mem, name),
			[]byte("---\ntype: derived\n---\nWould close a strange-loop if indexed."), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A legitimate file at root (user-profile.md is a real pattern): MUST
	// be indexed — only the named derivative views are excluded.
	if err := os.WriteFile(filepath.Join(mem, "user-profile.md"),
		[]byte("---\ntype: knowledge\n---\nLegitimate user info."), 0o644); err != nil {
		t.Fatal(err)
	}

	ix, err := Open(filepath.Join(mem, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := ix.Sync(context.Background(), mem); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	rows, err := ix.db.Query(`SELECT path FROM mem ORDER BY path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var indexed []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		indexed = append(indexed, filepath.Base(p))
	}

	// Must include the real mistake and the legitimate root file.
	wantIncluded := map[string]bool{"real.md": false, "user-profile.md": false}
	for _, b := range indexed {
		if _, ok := wantIncluded[b]; ok {
			wantIncluded[b] = true
		}
	}
	for name, seen := range wantIncluded {
		if !seen {
			t.Errorf("expected %q in index, got %v", name, indexed)
		}
	}

	// Must exclude the three derivative views.
	for _, excluded := range []string{"self-profile.md", "_agent_context.md", "MEMORY.md"} {
		for _, b := range indexed {
			if b == excluded {
				t.Errorf("%s MUST NOT be in FTS index (strange-loop hazard), but it is", excluded)
			}
		}
	}
}

// TestIsDerivativeView pins the exact set of excluded names so a future
// edit to the list (or a stray rename of the helper) fails the test.
func TestIsDerivativeView(t *testing.T) {
	cases := map[string]bool{
		"self-profile.md":   true,
		"_agent_context.md": true,
		"MEMORY.md":         true,
		"user-profile.md":   false,
		"real.md":           false,
		"self-profile":      false, // no .md suffix — not a markdown file
	}
	for name, want := range cases {
		if got := isDerivativeView(name); got != want {
			t.Errorf("isDerivativeView(%q) = %v, want %v", name, got, want)
		}
	}
}
