package native

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
)

// This file mirrors how Claude Code 2.1.284 resolves the auto-memory
// directory (class nS.resolveEntry/defaultPath in the bundled CLI). It is the
// only place that knows that rule; if a future harness changes it, change it
// here and re-verify against the binary. The harness NFC-normalizes the final
// path AFTER sanitizing, so it only touches the config-dir prefix (the slug is
// ASCII); that step is deliberately not replicated.

// maxSlugLen is the harness's cap on a sanitized project dir name; longer
// names keep the first maxSlugLen chars plus a hash suffix.
const maxSlugLen = 200

// SanitizePath encodes a path the way the harness names project dirs under
// ~/.claude/projects: every UTF-16 code unit outside [a-zA-Z0-9] becomes "-"
// (JS s.replace(/[^a-zA-Z0-9]/g,"-") — no /u flag, so an astral character is
// two dashes). A result longer than 200 is cut to 200 and suffixed with "-"
// plus base36(|javaHash(original)|).
func SanitizePath(s string) string {
	units := utf16.Encode([]rune(s))
	b := make([]byte, len(units))
	for i, u := range units {
		if u < 128 && isAlnum(byte(u)) {
			b[i] = byte(u)
		} else {
			b[i] = '-'
		}
	}
	if len(b) <= maxSlugLen {
		return string(b)
	}
	return string(b[:maxSlugLen]) + "-" + absBase36(javaHash(s))
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// javaHash is the harness's UJ(): h = (h<<5) - h + charCode | 0 over UTF-16
// code units, i.e. the 32-bit Java String.hashCode.
func javaHash(s string) int32 {
	var h int32
	for _, u := range utf16.Encode([]rune(s)) {
		h = h*31 + int32(u)
	}
	return h
}

// absBase36 is JS Math.abs(h).toString(36). Widened to int64 first so
// |MinInt32| does not overflow.
func absBase36(h int32) string {
	v := int64(h)
	if v < 0 {
		v = -v
	}
	return strconv.FormatInt(v, 36)
}

// CanonicalRoot mirrors the harness's canonical working-copy root: walk up
// from dir to the nearest .git entry (a directory or regular file; a
// symlinked .git is not a marker and ends the search). A .git FILE marks a
// linked worktree: follow "gitdir: <p>" and <p>/commondir to the shared git
// dir; when that dir is named ".git" the canonical root is its parent (the
// main checkout). Every read or parse failure keeps the worktree's own root.
// No repo → dir itself. Pure stat/read calls, no git subprocess.
func CanonicalRoot(dir string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	root := gitRoot(dir)
	if root == "" {
		return dir
	}
	return mainCheckout(root)
}

func gitRoot(dir string) string {
	for d := dir; ; {
		fi, err := os.Lstat(filepath.Join(d, ".git"))
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return ""
			}
			if fi.IsDir() || fi.Mode().IsRegular() {
				return d
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

func mainCheckout(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return root // .git is a directory (plain checkout) or unreadable
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, "gitdir:") {
		return root
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	c, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return root // submodule-style gitdir: its own project
	}
	common := strings.TrimSpace(string(c))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	common = filepath.Clean(common)
	if filepath.Base(common) != ".git" {
		// Bare repo with linked worktrees: the harness roots at the bare dir
		// unless it has its own .git entry.
		if _, err := os.Lstat(filepath.Join(common, ".git")); err == nil {
			return root
		}
		return common
	}
	return filepath.Dir(common)
}
