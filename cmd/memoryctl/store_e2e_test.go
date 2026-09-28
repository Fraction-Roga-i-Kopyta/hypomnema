package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

type storeFixture struct {
	claude, mem string
	env         map[string]string
}

func newStoreFixture(t *testing.T) storeFixture {
	t.Helper()
	home := t.TempDir()
	claude := filepath.Join(home, ".claude")
	mem := filepath.Join(claude, "memory")
	os.MkdirAll(mem, 0o755)
	os.WriteFile(filepath.Join(mem, ".wal"), nil, 0o644)
	return storeFixture{claude, mem, map[string]string{
		"CLAUDE_HOME": claude, "CLAUDE_MEMORY_DIR": mem, "HYPOMNEMA_TODAY": "2026-09-28",
	}}
}

func (f storeFixture) fact(t *testing.T, projectDir, name, body string) {
	t.Helper()
	dir := native.StoreFor(f.claude, projectDir).Dir
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, name+".md"),
		[]byte("---\nname: "+name+"\ntype: mistake\nkeywords: ["+name+"]\n---\n"+body+"\n"), 0o644)
}

func additionalContext(t *testing.T, out string) string {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return ""
	}
	var env struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad envelope %v: %s", err, out)
	}
	return env.HookSpecificOutput.AdditionalContext
}

func TestInject_ProjectDirBeatsDriftedCWD(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	out, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/somewhere-else","prompt":"dockercache"}`,
		"inject", "--event=UserPromptSubmit")
	if !strings.Contains(additionalContext(t, out), "docker layer cache") {
		t.Errorf("drifted cwd must not hide project facts; got %q", out)
	}
}

func TestInject_WorktreeReadsMainCheckoutStore(t *testing.T) {
	f := newStoreFixture(t)
	base := t.TempDir()
	mainRoot := filepath.Join(base, "main")
	mainGit := filepath.Join(mainRoot, ".git")
	// CanonicalRoot now also requires the resolved common dir to look like a
	// real git common dir (HEAD + objects/), not just sit at the right
	// position — see internal/native/store.go mainCheckout.
	os.MkdirAll(filepath.Join(mainGit, "objects"), 0o755)
	os.WriteFile(filepath.Join(mainGit, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	wtGit := filepath.Join(mainGit, "worktrees", "feat")
	os.MkdirAll(wtGit, 0o755)
	os.WriteFile(filepath.Join(wtGit, "commondir"), []byte("../..\n"), 0o644)
	wt := filepath.Join(mainRoot, ".worktrees", "feat")
	os.MkdirAll(wt, 0o755)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wtGit+"\n"), 0o644)
	// The back-link git writes for every linked worktree; CanonicalRoot now
	// requires it (see internal/native/store.go mainCheckout) as proof this
	// gitdir was actually linked to wt by git, not merely planted.
	os.WriteFile(filepath.Join(wtGit, "gitdir"), []byte(filepath.Join(wt, ".git")+"\n"), 0o644)
	f.fact(t, mainRoot, "worktreefact", "shared by worktrees")
	f.env["CLAUDE_PROJECT_DIR"] = wt
	out, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"`+wt+`","prompt":"worktreefact"}`,
		"inject", "--event=UserPromptSubmit")
	if !strings.Contains(additionalContext(t, out), "shared by worktrees") {
		t.Errorf("worktree session must read the main checkout's store; got %q", out)
	}
}

func TestRecall_UsesSessionPinFromSessionStart(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "pinnedfact", "found through the pin")
	start := map[string]string{}
	for k, v := range f.env {
		start[k] = v
	}
	start["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	runStdin(t, start, `{"session_id":"s-pin","cwd":"/tmp/proj","source":"startup"}`, "inject", "--event=SessionStart")

	cli := map[string]string{}
	for k, v := range f.env {
		cli[k] = v
	}
	cli["CLAUDE_CODE_SESSION_ID"] = "s-pin" // what the Bash tool exports; no CLAUDE_PROJECT_DIR
	out, errOut, code := run(t, cli, "recall", "pinnedfact")
	if code != 0 || !strings.Contains(out, "found through the pin") {
		t.Errorf("recall must resolve the pinned store; code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestGuard_BlocksAutoMemoryDirectoryStore(t *testing.T) {
	home := t.TempDir()
	claude := filepath.Join(home, ".claude")
	mem := filepath.Join(claude, "memory")
	custom := filepath.Join(home, "custom-mem")
	os.MkdirAll(mem, 0o755)
	os.MkdirAll(custom, 0o755)
	os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"autoMemoryDirectory":"`+custom+`"}`), 0o644)
	env := map[string]string{"CLAUDE_HOME": claude, "CLAUDE_MEMORY_DIR": mem, "CLAUDE_PROJECT_DIR": "/tmp/proj"}
	stdin := `{"cwd":"/tmp/proj","tool_name":"Write","tool_input":{"file_path":"` +
		filepath.Join(custom, "x.md") + `","content":"api_key: sk_live_abcd1234efgh"}}`
	if _, _, code := runStdin(t, env, stdin, "guard"); code != 2 {
		t.Fatalf("secret written into the autoMemoryDirectory store must block (exit 2), got %d", code)
	}
}

func TestInject_ConfigDirMovesOnlyTheProjectStore(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	mem := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(mem, 0o755)
	os.WriteFile(filepath.Join(mem, ".wal"), nil, 0o644)
	proj := native.StoreFor(cfg, "/tmp/proj").Dir
	os.MkdirAll(proj, 0o755)
	os.WriteFile(filepath.Join(proj, "cfgproj.md"),
		[]byte("---\nname: cfgproj\ntype: mistake\nkeywords: [cfgproj]\n---\nproject fact under config dir\n"), 0o644)
	glob := filepath.Join(home, ".claude", "memory-global")
	os.MkdirAll(glob, 0o755)
	os.WriteFile(filepath.Join(glob, "cfgglob.md"),
		[]byte("---\nname: cfgglob\ntype: mistake\nkeywords: [cfgproj]\n---\nglobal fact beside dot-claude\n"), 0o644)
	env := map[string]string{
		"HOME": home, "CLAUDE_HOME": "", "HYPOMNEMA_GLOBAL_DIR": "", "CLAUDE_CONFIG_DIR": cfg,
		"CLAUDE_MEMORY_DIR": mem, "CLAUDE_PROJECT_DIR": "/tmp/proj", "HYPOMNEMA_TODAY": "2026-09-28",
	}
	out, _, _ := runStdin(t, env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"cfgproj"}`,
		"inject", "--event=UserPromptSubmit")
	ctx := additionalContext(t, out)
	if !strings.Contains(ctx, "project fact under config dir") || !strings.Contains(ctx, "global fact beside dot-claude") {
		t.Errorf("CLAUDE_CONFIG_DIR must relocate projects/ only; ctx=%q", ctx)
	}
}
