package native

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSanitizePath_Parity(t *testing.T) {
	// Ground truth from the harness's own JS under node:
	//   PR("/Users/dev/Проекты/" + "sub_dir/".repeat(30))
	long := "/Users/dev/Проекты/" + strings.Repeat("sub_dir/", 30)
	longWant := "-Users-dev---------" + strings.Repeat("sub-dir-", 22) + "sub-d-p43ec2"
	cases := []struct{ name, in, want string }{
		{"repo", "/home/user/Development/hypomnema", "-home-user-Development-hypomnema"},
		{"tmp", "/tmp", "-tmp"},
		{"root", "/", "-"},
		{"dot worktree", "/r/.worktrees/wt-A", "-r--worktrees-wt-A"},
		{"underscore", "/r/my_proj", "-r-my-proj"},
		{"space", "/home/user/My Project", "-home-user-My-Project"},
		{"cyrillic one code unit each", "/r/проект", "-r-------"},
		{"astral char is two code units", "/r/\U0001F600", "-r---"},
		// >200: first 200 sanitized chars + "-" + base36(|javaHash(original)|);
		// expected value hardcoded from node, never recomputed by the code under test.
		{"long non-ASCII path hashed", long, longWant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizePath(tc.in); got != tc.want {
				t.Errorf("SanitizePath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Reference values computed with the harness's JS:
//
//	function UJ(t){let e=0;for(let n=0;n<t.length;n++)e=(e<<5)-e+t.charCodeAt(n)|0;return e}
//	Math.abs(UJ(s)).toString(36)
func TestJavaHash_KnownValues(t *testing.T) {
	cases := []struct {
		in   string
		want int32
	}{
		{"", 0},
		{"a", 97},
		{"hello", 99162322},
		{"/tmp", 1515144},
		{"/home/user/My Project", -978367768}, // int32 wrap
		{"/r/проект", -1377768948},            // non-ASCII code units
	}
	for _, tc := range cases {
		if got := javaHash(tc.in); got != tc.want {
			t.Errorf("javaHash(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
	// JS Math.abs(h).toString(36), verified with node.
	for in, want := range map[string]string{"/tmp": "wh3c", "/home/user/My Project": "g6hteg", "/r/проект": "msadbo"} {
		if got := absBase36(javaHash(in)); got != want {
			t.Errorf("absBase36(javaHash(%q)) = %q, want %q", in, got, want)
		}
	}
}

func mkGitDir(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// mkWorktree lays out what `git worktree add` writes: <wt>/.git is a file
// "gitdir: <main>/.git/worktrees/<name>", that dir holds commondir "../..",
// and (the back-link git writes for every linked worktree) a "gitdir" file
// naming <wt>/.git back. It also makes <mainRoot>/.git look like a real git
// common dir (HEAD + objects/), since CanonicalRoot now requires that too —
// self-sufficient regardless of whether the caller already created
// <mainRoot>/.git via mkGitDir.
func mkWorktree(t *testing.T, mainRoot, wt, name string) {
	t.Helper()
	mainGit := filepath.Join(mainRoot, ".git")
	if err := os.MkdirAll(filepath.Join(mainGit, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainGit, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wtGit := filepath.Join(mainGit, "worktrees", name)
	if err := os.MkdirAll(wtGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGit, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wtGit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGit, "gitdir"), []byte(filepath.Join(wt, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalRoot(t *testing.T) {
	base := t.TempDir()
	mainRoot := filepath.Join(base, "main")
	mkGitDir(t, mainRoot)
	sub := filepath.Join(mainRoot, "pkg", "deep")
	os.MkdirAll(sub, 0o755)
	wt := filepath.Join(mainRoot, ".worktrees", "feat")
	mkWorktree(t, mainRoot, wt, "feat")
	wtSub := filepath.Join(wt, "cmd")
	os.MkdirAll(wtSub, 0o755)
	plain := filepath.Join(base, "plain")
	os.MkdirAll(plain, 0o755)
	broken := filepath.Join(base, "broken")
	os.MkdirAll(broken, 0o755)
	os.WriteFile(filepath.Join(broken, ".git"), []byte("not a gitdir line\n"), 0o644)
	noCommon := filepath.Join(base, "submod")
	os.MkdirAll(filepath.Join(base, "modules", "x"), 0o755)
	os.MkdirAll(noCommon, 0o755)
	os.WriteFile(filepath.Join(noCommon, ".git"), []byte("gitdir: ../modules/x\n"), 0o644)

	cases := []struct{ name, in, want string }{
		{"repo root", mainRoot, mainRoot},
		{"repo subdir → git root", sub, mainRoot},
		{"worktree → main checkout", wt, mainRoot},
		{"worktree subdir → main checkout", wtSub, mainRoot},
		{"not a repo → itself", plain, plain},
		{"garbage .git file → own root", broken, broken},
		{"gitdir without commondir (submodule) → own root", noCommon, noCommon},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanonicalRoot(tc.in); got != tc.want {
				t.Errorf("CanonicalRoot(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCanonicalRoot_SymlinkGitStopsWalk(t *testing.T) {
	base := t.TempDir()
	outer := filepath.Join(base, "outer")
	mkGitDir(t, outer)
	inner := filepath.Join(outer, "inner")
	os.MkdirAll(inner, 0o755)
	target := filepath.Join(base, "elsewhere")
	os.MkdirAll(target, 0o755)
	if err := os.Symlink(target, filepath.Join(inner, ".git")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	// Mirrors the harness: a symlinked .git is not a repo marker and ends the
	// search (no git root) — the dir resolves to itself, not to outer.
	if got := CanonicalRoot(inner); got != inner {
		t.Errorf("CanonicalRoot(symlinked .git) = %q, want %q", got, inner)
	}
}

// TestCanonicalRoot_HostilePointers covers the milestone-1 domain-review
// finding: mainCheckout used to trust planted .git/commondir content with
// no proof git itself wrote it, so a bare tarball/zip extraction (no real
// git involved) could redirect CanonicalRoot onto an unrelated, pre-existing
// directory — cross-project memory disclosure, or a shared junk slug. Every
// subtest plants a ".git file → gitdir dir" pair by hand and asserts the
// result stays the planted root's OWN root.
func TestCanonicalRoot_HostilePointers(t *testing.T) {
	// plantPointer writes root/.git → "gitdir: <p>" and returns p (a fresh,
	// empty directory the subtest then populates with commondir / gitdir).
	plantPointer := func(t *testing.T, base string) (root, p string) {
		t.Helper()
		root = filepath.Join(base, "hostile-root")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		p = filepath.Join(base, "gitdir")
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+p+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return root, p
	}
	writeBacklink := func(t *testing.T, p, root string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(p, "gitdir"), []byte(filepath.Join(root, ".git")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	assertOwnRoot := func(t *testing.T, root string) {
		t.Helper()
		if got := CanonicalRoot(root); got != root {
			t.Errorf("CanonicalRoot(%q) = %q, want own root %q", root, got, root)
		}
	}

	t.Run("commondir slash with matching backlink", func(t *testing.T) {
		// Proves the back-link check alone (root <-> p) is not what saves
		// this: the back-link genuinely matches, yet a bare existence +
		// basename check on commondir="/" would still let this through.
		base := t.TempDir()
		root, p := plantPointer(t, base)
		writeBacklink(t, p, root)
		os.WriteFile(filepath.Join(p, "commondir"), []byte("/\n"), 0o644)
		assertOwnRoot(t, root)
	})

	t.Run("commondir slash without backlink", func(t *testing.T) {
		base := t.TempDir()
		root, p := plantPointer(t, base)
		os.WriteFile(filepath.Join(p, "commondir"), []byte("/\n"), 0o644)
		assertOwnRoot(t, root)
	})

	t.Run("commondir names another repo, no backlink", func(t *testing.T) {
		base := t.TempDir()
		other := filepath.Join(base, "other-real-repo")
		mkGitDir(t, other)
		root, p := plantPointer(t, base)
		os.WriteFile(filepath.Join(p, "commondir"), []byte(filepath.Join(other, ".git")+"\n"), 0o644)
		assertOwnRoot(t, root)
	})

	t.Run("backlink names a different worktree path", func(t *testing.T) {
		base := t.TempDir()
		root, p := plantPointer(t, base)
		otherRoot := filepath.Join(base, "some-other-worktree")
		os.MkdirAll(otherRoot, 0o755)
		os.WriteFile(filepath.Join(p, "gitdir"), []byte(filepath.Join(otherRoot, ".git")+"\n"), 0o644)
		os.WriteFile(filepath.Join(p, "commondir"), []byte("../..\n"), 0o644)
		assertOwnRoot(t, root)
	})

	t.Run("commondir names a nonexistent repo", func(t *testing.T) {
		base := t.TempDir()
		root, p := plantPointer(t, base)
		writeBacklink(t, p, root)
		nonexistent := filepath.Join(base, "does-not-exist", ".git")
		os.WriteFile(filepath.Join(p, "commondir"), []byte(nonexistent+"\n"), 0o644)
		assertOwnRoot(t, root)
	})

	t.Run("oversized git file", func(t *testing.T) {
		base := t.TempDir()
		root := filepath.Join(base, "hostile-root")
		os.MkdirAll(root, 0o755)
		huge := "gitdir: " + strings.Repeat("a", 5000)
		os.WriteFile(filepath.Join(root, ".git"), []byte(huge), 0o644)
		assertOwnRoot(t, root)
	})

	// The next two prove position (Dir(Dir(p))==common) is not enough on its
	// own: <p> is itself attacker-chosen, so a shallow enough <p> makes its
	// grandparent land on some existing directory the attacker never wrote
	// HEAD/objects into. Both use a genuinely matching back-link and a
	// genuinely "../.."-consistent commondir, so only the new HEAD+objects
	// check is what rejects them.

	t.Run("common exists at the right position but isn't a real git dir (archive parent)", func(t *testing.T) {
		// Analogous to a one-folder archive extracted into ~/Downloads: the
		// archive's single top-level folder holds root and p as siblings, so
		// Dir(Dir(p)) lands exactly on the archive's parent (~/Downloads
		// here) — a pre-existing directory the tarball never wrote HEAD or
		// objects/ into.
		downloads := t.TempDir()
		archive := filepath.Join(downloads, "archive")
		root := filepath.Join(archive, "hostile-root")
		p := filepath.Join(archive, "gitdir")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+p+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeBacklink(t, p, root)
		if err := os.WriteFile(filepath.Join(p, "commondir"), []byte("../..\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		assertOwnRoot(t, root)
	})

	t.Run("common resolves a level higher, analogous to /", func(t *testing.T) {
		// Analogous to a flat archive extracted directly into /tmp (no
		// intermediate single-folder): root and p sit directly under the
		// extraction target, one level shallower than the previous subtest,
		// so Dir(Dir(p)) lands on the extraction target's OWN parent —
		// standing in for "/" itself, since a test can't write into the
		// real filesystem root. The parent of this subtest's own
		// t.TempDir() is exactly such a pre-existing, HEAD/objects-less
		// ancestor.
		tmp := t.TempDir()
		root := filepath.Join(tmp, "hostile-root")
		p := filepath.Join(tmp, "gitdir")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+p+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeBacklink(t, p, root)
		if err := os.WriteFile(filepath.Join(p, "commondir"), []byte("../..\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		assertOwnRoot(t, root)
	})
}

func fakeEnv(claudeDir, projectDir string, env map[string]string, files map[string]string) StoreEnv {
	return StoreEnv{
		ClaudeDir: claudeDir, ProjectDir: projectDir, UserHome: "/home/u",
		ManagedSettingsPath: "/managed/managed-settings.json",
		Getenv:              func(k string) string { return env[k] },
		ReadFile: func(p string) ([]byte, error) {
			if s, ok := files[p]; ok {
				return []byte(s), nil
			}
			return nil, os.ErrNotExist
		},
	}
}

func TestResolveStore_Default(t *testing.T) {
	st := ResolveStore(fakeEnv("/home/u/.claude", "/tmp/proj", nil, nil))
	want := Store{
		Dir:     filepath.Join("/home/u/.claude", "projects", "-tmp-proj", "memory"),
		Project: "-tmp-proj", Root: "/tmp/proj", Source: "default",
	}
	if st != want {
		t.Errorf("got %+v, want %+v", st, want)
	}
	if sc := st.Scope(); len(sc) != 2 || sc[0] != "-tmp-proj" || sc[1] != GlobalProject {
		t.Errorf("Scope() = %v", sc)
	}
}

func TestResolveStore_WorktreeSharesMainStore(t *testing.T) {
	base := t.TempDir()
	mainRoot := filepath.Join(base, "main")
	mkGitDir(t, mainRoot)
	wt := filepath.Join(mainRoot, ".worktrees", "feat")
	mkWorktree(t, mainRoot, wt, "feat")
	a := ResolveStore(fakeEnv("/c", mainRoot, nil, nil))
	b := ResolveStore(fakeEnv("/c", wt, nil, nil))
	if a.Dir != b.Dir || a.Project != b.Project {
		t.Errorf("worktree store %+v != main store %+v", b, a)
	}
}

func TestResolveStore_Overrides(t *testing.T) {
	user := "/home/u/.claude/settings.json"
	local := "/tmp/proj/.claude/settings.local.json"
	managed := "/managed/managed-settings.json"
	cases := []struct {
		name       string
		env        map[string]string
		files      map[string]string
		wantDir    string
		wantSource string
	}{
		{"env override wins", map[string]string{"CLAUDE_COWORK_MEMORY_PATH_OVERRIDE": "/cowork/mem"},
			map[string]string{user: `{"autoMemoryDirectory":"/u/mem"}`}, "/cowork/mem", "env-override"},
		{"policy before local and user", nil, map[string]string{
			managed: `{"autoMemoryDirectory":"/p/mem"}`, local: `{"autoMemoryDirectory":"/l/mem"}`,
			user: `{"autoMemoryDirectory":"/u/mem"}`}, "/p/mem", "settings:policy"},
		{"local before user", nil, map[string]string{
			local: `{"autoMemoryDirectory":"/l/mem"}`, user: `{"autoMemoryDirectory":"/u/mem"}`},
			"/l/mem", "settings:local"},
		{"user", nil, map[string]string{user: `{"autoMemoryDirectory":"~/mem"}`},
			"/home/u/mem", "settings:user"},
		// Harness parity: the FIRST scope that sets the key decides; an
		// invalid value falls back to the default store, not the next scope.
		{"invalid first key → default", nil, map[string]string{
			local: `{"autoMemoryDirectory":"relative/dir"}`, user: `{"autoMemoryDirectory":"/u/mem"}`},
			filepath.Join("/home/u/.claude", "projects", "-tmp-proj", "memory"), "default"},
		{"filesystem root rejected", nil, map[string]string{user: `{"autoMemoryDirectory":"/"}`},
			filepath.Join("/home/u/.claude", "projects", "-tmp-proj", "memory"), "default"},
		{"malformed json skipped", nil, map[string]string{user: `{not json`},
			filepath.Join("/home/u/.claude", "projects", "-tmp-proj", "memory"), "default"},
		{"checked-in project settings ignored", nil, map[string]string{
			"/tmp/proj/.claude/settings.json": `{"autoMemoryDirectory":"/evil"}`},
			filepath.Join("/home/u/.claude", "projects", "-tmp-proj", "memory"), "default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := ResolveStore(fakeEnv("/home/u/.claude", "/tmp/proj", tc.env, tc.files))
			if st.Dir != tc.wantDir || st.Source != tc.wantSource {
				t.Errorf("got Dir=%q Source=%q, want Dir=%q Source=%q", st.Dir, st.Source, tc.wantDir, tc.wantSource)
			}
			if tc.wantSource != "default" && st.Project != SanitizePath(tc.wantDir) {
				t.Errorf("override project tag = %q, want SanitizePath(dir) %q", st.Project, SanitizePath(tc.wantDir))
			}
		})
	}
}

// withTimeout runs fn in a goroutine and fails the test if it does not
// return within d — a planted FIFO must not hang store resolution (os.Open
// on a FIFO with no writer blocks indefinitely).
func withTimeout(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("did not return within %s — likely blocked on a planted FIFO", d)
	}
}

func TestCanonicalRoot_FIFOBacklinkDoesNotHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs on windows")
	}
	base := t.TempDir()
	mainRoot := filepath.Join(base, "main")
	mkGitDir(t, mainRoot)
	wt := filepath.Join(mainRoot, ".worktrees", "feat")
	mkWorktree(t, mainRoot, wt, "feat")

	// Replace the worktree back-link (<p>/gitdir) with a FIFO. Without
	// readPointerFile using O_NONBLOCK, os.Open on this blocks until a
	// writer attaches — up to 78s observed in practice, effectively forever
	// in a hook's timeout budget.
	backlink := filepath.Join(mainRoot, ".git", "worktrees", "feat", "gitdir")
	if err := os.Remove(backlink); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(backlink, 0o644); err != nil {
		t.Fatal(err)
	}

	var got string
	withTimeout(t, 2*time.Second, func() { got = CanonicalRoot(wt) })
	if got != wt {
		t.Errorf("CanonicalRoot(FIFO back-link) = %q, want own root %q", got, wt)
	}
}

func TestResolveStore_FIFOSettingsFileDoesNotHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs on windows")
	}
	base := t.TempDir()
	claudeDir := filepath.Join(base, ".claude")
	projectDir := filepath.Join(base, "proj")
	if err := os.MkdirAll(filepath.Join(projectDir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(projectDir, ".claude", "settings.local.json")
	if err := syscall.Mkfifo(settingsPath, 0o644); err != nil {
		t.Fatal(err)
	}

	var st Store
	withTimeout(t, 2*time.Second, func() {
		st = ResolveStore(StoreEnv{
			ClaudeDir: claudeDir, ProjectDir: projectDir,
			ManagedSettingsPath: filepath.Join(base, "does-not-exist.json"),
		})
	})
	if st.Source != "default" {
		t.Errorf("FIFO settings file: got Source=%q, want default (unreadable → fallback)", st.Source)
	}
}

func TestResolveStore_OversizedSettingsFileRejected(t *testing.T) {
	base := t.TempDir()
	claudeDir := filepath.Join(base, ".claude")
	projectDir := filepath.Join(base, "proj")
	if err := os.MkdirAll(filepath.Join(projectDir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(projectDir, ".claude", "settings.local.json")
	f, err := os.Create(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: does not actually write 1 MiB+1 to disk.
	if err := f.Truncate(1<<20 + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	st := ResolveStore(StoreEnv{
		ClaudeDir: claudeDir, ProjectDir: projectDir,
		ManagedSettingsPath: filepath.Join(base, "does-not-exist.json"),
	})
	if st.Source != "default" {
		t.Errorf("oversized settings file: got Source=%q, want default (over-cap → fallback)", st.Source)
	}
}
