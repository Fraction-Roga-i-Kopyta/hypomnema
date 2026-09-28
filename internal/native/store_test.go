package native

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
//   function UJ(t){let e=0;for(let n=0;n<t.length;n++)e=(e<<5)-e+t.charCodeAt(n)|0;return e}
//   Math.abs(UJ(s)).toString(36)
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
// "gitdir: <main>/.git/worktrees/<name>", and that dir holds commondir "../..".
func mkWorktree(t *testing.T, mainRoot, wt, name string) {
	t.Helper()
	wtGit := filepath.Join(mainRoot, ".git", "worktrees", name)
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
		Dir: filepath.Join("/home/u/.claude", "projects", "-tmp-proj", "memory"),
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
