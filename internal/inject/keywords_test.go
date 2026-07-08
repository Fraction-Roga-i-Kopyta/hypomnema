package inject

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestKeywords(t *testing.T) {
	got := Keywords("/home/user/dev/my-project", "help with docker cache layer")
	set := map[string]bool{}
	for _, k := range got {
		set[k] = true
	}
	for _, want := range []string{"docker", "cache", "layer", "help", "with"} {
		if !set[want] {
			t.Errorf("expected prompt token %q in %v", want, got)
		}
	}
	if !set["project"] {
		t.Errorf("expected basename token 'project' in %v", got)
	}
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			t.Errorf("duplicate token %q", sorted[i])
		}
	}
}

func TestKeywords_EmptyPrompt(t *testing.T) {
	got := Keywords("/tmp/alpha-svc", "")
	set := map[string]bool{}
	for _, k := range got {
		set[k] = true
	}
	if !set["alpha"] && !set["svc"] {
		t.Errorf("expected basename tokens from /tmp/alpha-svc, got %v", got)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func has(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// At SessionStart the prompt is empty; the relevance signal should include the
// project context — branch name and changed files — not just the cwd basename.
func TestKeywords_IncludesGitBranchAndChangedFiles(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "init")
	gitRun(t, repo, "checkout", "-q", "-b", "loginflow")
	if err := os.WriteFile(filepath.Join(repo, "payments.go"), []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}

	kw := Keywords(repo, "")

	if !has(kw, "loginflow") {
		t.Errorf("expected branch token 'loginflow' in %v", kw)
	}
	if !has(kw, "payments") {
		t.Errorf("expected changed-file token 'payments' in %v", kw)
	}
}

// A non-git directory must not error — git enrichment is best-effort.
func TestKeywords_NonGitDirIsSafe(t *testing.T) {
	dir := t.TempDir()
	kw := Keywords(dir, "deploy the cache")
	if !has(kw, "deploy") || !has(kw, "cache") {
		t.Errorf("prompt tokens must survive in non-git dir; got %v", kw)
	}
}

func TestGitSignal_BoundedByTimeout(t *testing.T) {
	// A hung git (lock contention, network mount) must not stall the
	// SessionStart hook: gitSignal is best-effort context, not a blocker.
	bin := t.TempDir()
	stub := "#!/bin/sh\n/bin/sleep 3\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	start := time.Now()
	gitSignal(t.TempDir())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("gitSignal took %v — git calls are unbounded", elapsed)
	}
}
