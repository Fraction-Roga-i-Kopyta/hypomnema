package native

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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
// main checkout). No repo → dir itself. Pure stat/read calls, no git
// subprocess.
//
// Every pointer file mainCheckout follows is capped at maxPointerFileBytes
// and must additionally prove git itself produced it — the worktree
// back-link at <p>/gitdir must name root/.git, and the resolved common dir
// must exist and sit exactly where that back-link implies. See
// mainCheckout's doc comment for the full reasoning: planted pointer files
// (a tarball/zip extraction needs no real git to create a "commondir"
// naming "/" or another repo) must not redirect which project's memory
// store a session reads and writes. Every read, parse, or validation
// failure keeps the worktree's own root.
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
			// Anything else (FIFO, device, ...) is not a marker either — fall
			// through and keep climbing, mirroring the harness's async walk:
			// only a dir or regular file marks a root, only a symlink stops it.
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// maxPointerFileBytes bounds every read of a git pointer file mainCheckout
// follows (.git, commondir, the worktree gitdir back-link). Genuine git
// installs never put more than a path and a trailing newline in any of
// these; a planted multi-GB file at one of these names — no real git
// required, just a tarball/zip extraction — must not be pulled into memory
// on every hook invocation.
const maxPointerFileBytes = 4096

// readPointerFile reads path capped at maxPointerFileBytes. ok is false for
// any read error AND for a file that exceeds the cap — the latter is
// deliberately not truncated-and-parsed (a truncated line could still
// happen to parse into attacker-chosen content); both cases fall back to
// "unreadable" exactly like a missing file.
func readPointerFile(path string) (content string, ok bool) {
	data, err := readRegularFile(path, maxPointerFileBytes)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// maxSettingsFileBytes bounds every read of a settings.json scope
// (managed/local/user) during store resolution. A genuine settings file is
// a small JSON document; a planted multi-GB or sparse-huge one at one of
// these well-known paths must not be pulled into memory on every hook
// invocation just to look for one key.
const maxSettingsFileBytes = 1 << 20 // 1 MiB

// readRegularFile reads path capped at max bytes, refusing anything that
// is not a plain regular file. It opens with O_NONBLOCK so a FIFO planted
// at path cannot hang the caller: a blocking os.Open (no O_NONBLOCK) on a
// FIFO with no writer attached waits indefinitely (78s+ observed) for one
// to show up, which would stall a hook past its timeout budget. An
// O_RDONLY|O_NONBLOCK open of a FIFO succeeds immediately regardless of
// whether a writer is attached — ENXIO is a write-side condition (an
// O_WRONLY|O_NONBLOCK open with no reader on the other end) that never
// applies to this read-only path — so O_NONBLOCK alone does not keep a
// planted FIFO out; it is the follow-up Mode().IsRegular() check that
// rejects it (and any other non-regular file — device, socket, …) the open
// did succeed against. A read of more than max bytes is an error, not a
// silent truncation — a truncated pointer-file line could still happen to
// parse into attacker-chosen content.
func readRegularFile(path string, max int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New(path + ": not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New(path + ": exceeds byte cap")
	}
	return data, nil
}

// samePath reports whether a and b (both already filepath.Clean-ed by the
// caller) name the same file: first by exact string match, and only if that
// fails, by resolving symlinks on both sides. Either side failing to
// resolve counts as "not the same" — a read error never upgrades an
// unverifiable path into a match.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// mainCheckout resolves a linked worktree's root (dir) to its main
// checkout: .git is a file naming a gitdir <p> ("gitdir: <p>"), and
// <p>/commondir names the shared .git dir. Every one of these is content
// mainCheckout reads and trusts, so each step below additionally requires
// proof that git itself produced it — not merely a planted file with the
// right name, which a tarball/zip extraction can create with no real git
// involved (the finding this fixes: such a file could force the resolved
// root to "/" or to an unrelated, pre-existing repo, redirecting a session
// onto another project's memory store):
//
//   - every read (.git, the gitdir back-link, commondir) goes through
//     readPointerFile, capped at maxPointerFileBytes; oversized or
//     unreadable is treated as absent → own root.
//   - <p>/gitdir must exist and, resolved, name root/.git — the back-link
//     git writes into every linked worktree's
//     <main>/.git/worktrees/<name>/gitdir. Its absence or mismatch means
//     <p> was never linked to root by git → own root.
//   - the resolved common dir must exist and be a directory (os.Stat +
//     IsDir); a commondir naming a path that doesn't exist must not become
//     the store's new root.
//   - the resolved common dir must also be exactly two levels above <p> —
//     the "../.." every git-created commondir file holds for a linked
//     worktree. Without this, a commondir value can still redirect to any
//     directory that happens to exist (including "/") even when the
//     back-link above was itself forged to self-consistently match root:
//     the attacker only controls where <p> itself sits, so pinning common
//     to <p>'s structural position rules out redirecting to a pre-existing,
//     unrelated directory (another repo, "/", ...).
//   - the resolved common dir must also look like a real git common dir:
//     <common>/HEAD exists as a regular file or symlink, and
//     <common>/objects exists as a directory. Position alone (the check
//     above) is not enough, because <p> is itself attacker-chosen: a
//     shallow-enough <p> makes its two-levels-up ancestor land on some
//     existing, pre-existing directory the attacker never wrote to at all
//     (the extraction target's own parent — e.g. ~/Downloads for a
//     one-folder archive, or "/" for a flat archive extracted straight into
//     /tmp). HEAD and objects/ are ground truth a plain ancestor directory
//     never has; requiring both closes that gap regardless of where the
//     attacker places <p>.
//
// Any failure at any step keeps the worktree's own root. The bare-repo
// branch below (common has no nested .git of its own) and every other
// existing "failure → own root" behavior is unchanged.
func mainCheckout(root string) string {
	raw, ok := readPointerFile(filepath.Join(root, ".git"))
	if !ok {
		return root // .git is a directory (plain checkout), unreadable, or oversized
	}
	line := strings.TrimSpace(raw)
	if !strings.HasPrefix(line, "gitdir:") {
		return root
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	gitdir = filepath.Clean(gitdir)

	back, ok := readPointerFile(filepath.Join(gitdir, "gitdir"))
	if !ok {
		return root // no worktree back-link: <p> was never linked to root by git
	}
	backPath := strings.TrimSpace(back)
	if !filepath.IsAbs(backPath) {
		backPath = filepath.Join(gitdir, backPath)
	}
	backPath = filepath.Clean(backPath)
	if !samePath(backPath, filepath.Join(root, ".git")) {
		return root
	}

	c, ok := readPointerFile(filepath.Join(gitdir, "commondir"))
	if !ok {
		return root // submodule-style gitdir, unreadable, or oversized commondir
	}
	common := strings.TrimSpace(c)
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	common = filepath.Clean(common)

	if fi, err := os.Stat(common); err != nil || !fi.IsDir() {
		return root
	}
	if !samePath(common, filepath.Dir(filepath.Dir(gitdir))) {
		return root
	}
	// common's position checks out, but position alone is attacker-chosen
	// (via <p>'s own location) — require it to actually look like a git
	// common dir too: HEAD and objects/ are what a plain ancestor directory
	// (~/Downloads, "/", ...) never has.
	if fi, err := os.Lstat(filepath.Join(common, "HEAD")); err != nil ||
		!(fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0) {
		return root
	}
	if fi, err := os.Stat(filepath.Join(common, "objects")); err != nil || !fi.IsDir() {
		return root
	}

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

// Store is the native memory store a session reads and writes.
type Store struct {
	Dir     string // absolute memory dir
	Project string // sidecar/WAL project tag (QKey prefix)
	Root    string // canonical project root ("" when an override picked Dir)
	Source  string // default | env-override | settings:policy|local|user
}

// Scope is the sidecar reconciliation scope: this store's project + global.
func (s Store) Scope() []string { return []string{s.Project, GlobalProject} }

// StoreEnv is everything ResolveStore reads. Zero values fall back to the
// process: os.Getenv, a capped regular-file reader (readRegularFile at
// maxSettingsFileBytes — not os.ReadFile, so a FIFO or an oversized file
// planted at a settings path can't hang or balloon a hook), os.UserHomeDir,
// the platform managed-settings path.
type StoreEnv struct {
	ClaudeDir           string // Claude Code config dir; projects/ and settings.json live here
	ProjectDir          string // project anchor (absolute)
	UserHome            string
	ManagedSettingsPath string
	Getenv              func(string) string
	ReadFile            func(string) ([]byte, error)
}

// StoreFor resolves the store for projectDir with process defaults.
func StoreFor(claudeDir, projectDir string) Store {
	return ResolveStore(StoreEnv{ClaudeDir: claudeDir, ProjectDir: projectDir})
}

// ResolveStore mirrors the harness: CLAUDE_COWORK_MEMORY_PATH_OVERRIDE, then
// autoMemoryDirectory from the first settings scope that sets it (policy →
// project-local → user; the checked-in project settings.json is ignored, as
// the harness documents), then <ClaudeDir>/projects/<SanitizePath(
// CanonicalRoot(ProjectDir))>/memory. An override's project tag is
// SanitizePath(dir): identity follows the store, so several projects sharing
// one override dir are one store.
func ResolveStore(e StoreEnv) Store {
	getenv, read, home := e.Getenv, e.ReadFile, e.UserHome
	if getenv == nil {
		getenv = os.Getenv
	}
	if read == nil {
		read = func(p string) ([]byte, error) { return readRegularFile(p, maxSettingsFileBytes) }
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if d := validMemoryDir(getenv("CLAUDE_COWORK_MEMORY_PATH_OVERRIDE"), "", false); d != "" {
		return Store{Dir: d, Project: SanitizePath(d), Source: "env-override"}
	}
	managed := e.ManagedSettingsPath
	if managed == "" {
		managed = defaultManagedSettingsPath()
	}
	scopes := []struct{ name, path string }{
		{"policy", managed},
		{"local", filepath.Join(e.ProjectDir, ".claude", "settings.local.json")},
		{"user", filepath.Join(e.ClaudeDir, "settings.json")},
	}
	for _, sc := range scopes {
		raw, err := read(sc.path)
		if err != nil {
			continue
		}
		var s struct {
			AutoMemoryDirectory *string `json:"autoMemoryDirectory"`
		}
		if json.Unmarshal(raw, &s) != nil || s.AutoMemoryDirectory == nil {
			continue
		}
		if d := validMemoryDir(*s.AutoMemoryDirectory, home, true); d != "" {
			return Store{Dir: d, Project: SanitizePath(d), Source: "settings:" + sc.name}
		}
		break // first scope that sets the key decides; invalid → default
	}
	root := CanonicalRoot(e.ProjectDir)
	slug := SanitizePath(root)
	return Store{
		Dir:     filepath.Join(e.ClaudeDir, "projects", slug, "memory"),
		Project: slug, Root: root, Source: "default",
	}
}

// validMemoryDir mirrors the harness's path check: "~/" expands to home when
// allowed; empty, NUL-bearing, relative, too-short and filesystem-root values
// are rejected ("").
func validMemoryDir(p, home string, expandTilde bool) string {
	p = strings.TrimSpace(p)
	if expandTilde && home != "" && strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[2:])
	}
	if len(p) < 3 || strings.ContainsRune(p, 0) || !filepath.IsAbs(p) {
		return ""
	}
	p = filepath.Clean(p)
	if filepath.Dir(p) == p {
		return ""
	}
	return p
}

func defaultManagedSettingsPath() string {
	if runtime.GOOS == "darwin" {
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	}
	return "/etc/claude-code/managed-settings.json"
}
