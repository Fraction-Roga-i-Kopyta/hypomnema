package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func checkClaudeDir(dir string) Check {
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return Check{Name: "claude_dir_exists", Status: OK, Detail: dir}
	}
	return Check{
		Name:   "claude_dir_exists",
		Status: FAIL,
		Detail: fmt.Sprintf("%s is missing — hypomnema is not installed", dir),
	}
}

// checkMemoryDir verifies the v2 metadata root exists. In v2 this directory
// holds only metadata (.wal, .sidecar.db) — memory content lives in native
// files elsewhere, so there are no required subdirectories. A missing .wal
// on a fresh install is normal and is merely noted, never a failure.
func checkMemoryDir(dir string) Check {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return Check{
			Name:   "memory_dir_exists",
			Status: FAIL,
			Detail: fmt.Sprintf("%s missing — run install.sh", dir),
		}
	}
	detail := dir
	if _, err := os.Stat(filepath.Join(dir, ".wal")); err == nil {
		detail += " (.wal present)"
	} else {
		detail += " (no .wal yet — fresh install)"
	}
	return Check{Name: "memory_dir_exists", Status: OK, Detail: detail}
}

// requiredHookCommands is the set install.sh wires up on a fresh install,
// as the suffix of the v2 shim path "hooks/v2/<name>". Kept in one place
// so the list stays in lockstep with install.sh.
var requiredHookCommands = []string{
	"session-start.sh",
	"user-prompt-submit.sh",
	"pre-tool-write.sh",
	"skill-learnings-inject.sh",
	"skill-active.sh",
	"session-stop.sh",
	"subagent-start.sh",
	"subagent-stop.sh",
}

// shimEvent maps each required v2 shim to the hook event install.sh registers
// it under. Kept in lockstep with install.sh's register_hook calls; checked by
// checkSettings so a shim wired to the WRONG event (e.g. session-start under
// Stop) is caught, not just a missing one (review O4).
var shimEvent = map[string]string{
	"session-start.sh":          "SessionStart",
	"user-prompt-submit.sh":     "UserPromptSubmit",
	"pre-tool-write.sh":         "PreToolUse",
	"skill-active.sh":           "PreToolUse",
	"skill-learnings-inject.sh": "PostToolUse",
	"session-stop.sh":           "Stop",
	"subagent-start.sh":         "SubagentStart",
	"subagent-stop.sh":          "SubagentStop",
}

func checkSettings(path string) Check {
	const name = "settings_hooks_registered"
	data, err := os.ReadFile(path)
	if err != nil {
		return Check{Name: name, Status: FAIL, Detail: fmt.Sprintf("%s unreadable: %v", path, err)}
	}
	// Parse the hooks structure rather than substring-matching: a corrupt
	// settings.json (which Claude Code cannot load, so NO hook runs) or a shim
	// wired under the wrong event would both pass a substring scan (review O4).
	var parsed struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Check{Name: name, Status: FAIL, Detail: fmt.Sprintf("%s is not valid JSON (%v) — Claude Code cannot load it, so NO hooks run; fix or restore a backup", path, err)}
	}
	// Build event → set of registered command strings.
	byEvent := map[string][]string{}
	for event, entries := range parsed.Hooks {
		for _, e := range entries {
			for _, h := range e.Hooks {
				byEvent[event] = append(byEvent[event], h.Command)
			}
		}
	}
	var problems []string
	for _, shim := range requiredHookCommands {
		wantEvent := shimEvent[shim]
		found := false
		for _, cmd := range byEvent[wantEvent] {
			if strings.Contains(cmd, "hooks/v2/"+shim) {
				found = true
				break
			}
		}
		if !found {
			// Is it registered under some OTHER event (misconfigured) vs absent?
			misplaced := ""
			for event, cmds := range byEvent {
				for _, cmd := range cmds {
					if strings.Contains(cmd, "hooks/v2/"+shim) {
						misplaced = event
					}
				}
			}
			if misplaced != "" {
				problems = append(problems, fmt.Sprintf("%s under %s (want %s)", shim, misplaced, wantEvent))
			} else {
				problems = append(problems, fmt.Sprintf("%s missing (want %s)", shim, wantEvent))
			}
		}
	}
	if len(problems) == 0 {
		return Check{Name: name, Status: OK, Detail: fmt.Sprintf("all %d hypomnema hooks registered under the right events", len(requiredHookCommands))}
	}
	return Check{Name: name, Status: FAIL, Detail: strings.Join(problems, "; ") + " — re-run ./install.sh"}
}

// checkShimFiles verifies each required v2 shim EXISTS and is EXECUTABLE at
// <claudeDir>/hooks/v2/. checkSettings only substring-matches settings.json,
// so without this check a wiped hooks/v2/ directory reads as fully healthy —
// the false-OK found by the 2026-07-08 review.
func checkShimFiles(claudeDir string) Check {
	const name = "shim_files_present"
	dir := filepath.Join(claudeDir, "hooks", "v2")
	var missing, notExec []string
	for _, cmd := range requiredHookCommands {
		info, err := os.Stat(filepath.Join(dir, cmd))
		if err != nil || !info.Mode().IsRegular() {
			// A directory also carries exec bits, so IsRegular is required or a
			// shim replaced by a directory reads as "present executable" (R3).
			missing = append(missing, cmd)
			continue
		}
		if info.Mode()&0o111 == 0 {
			notExec = append(notExec, cmd)
		}
	}
	if len(missing) == 0 && len(notExec) == 0 {
		return Check{
			Name:   name,
			Status: OK,
			Detail: fmt.Sprintf("all %d shims present and executable in %s", len(requiredHookCommands), dir),
		}
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "missing: "+strings.Join(missing, ","))
	}
	if len(notExec) > 0 {
		parts = append(parts, "not executable: "+strings.Join(notExec, ","))
	}
	return Check{
		Name:   name,
		Status: FAIL,
		Detail: strings.Join(parts, "; ") + " — re-run ./install.sh",
	}
}

// checkBrokenSymlinks walks dir (one level) and reports symlinks whose
// targets do not resolve. label is "hooks" or "bin" for the message.
func checkBrokenSymlinks(dir, label string) Check {
	name := "no_broken_symlinks_" + label
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{Name: name, Status: WARN, Detail: dir + " absent"}
		}
		return Check{Name: name, Status: WARN, Detail: err.Error()}
	}
	broken := []string{}
	total := 0
	for _, e := range entries {
		info, err := os.Lstat(filepath.Join(dir, e.Name()))
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		total++
		if _, err := os.Stat(filepath.Join(dir, e.Name())); err != nil {
			broken = append(broken, e.Name())
		}
	}
	if len(broken) == 0 {
		return Check{Name: name, Status: OK, Detail: fmt.Sprintf("%d symlinks, all resolve", total)}
	}
	return Check{
		Name:   name,
		Status: FAIL,
		Detail: fmt.Sprintf("%d broken: %s", len(broken), strings.Join(broken, ",")),
	}
}

func checkMemoryctl(claudeDir string) Check {
	// First preference: the install.sh target (~/.claude/bin/memoryctl).
	// If that's present the PATH question is moot for hypomnema itself;
	// PATH only matters for users running `memoryctl ...` in a shell.
	local := filepath.Join(claudeDir, "bin", "memoryctl")
	info, err := os.Stat(local)
	if err != nil || info.IsDir() {
		return Check{
			Name:   "memoryctl_available",
			Status: WARN,
			Detail: "memoryctl binary not built — run `make build` then ./install.sh",
		}
	}
	// Present, but is it a working binary? os.Stat alone let a symlink to a
	// text file read as healthy — verify the exec bit AND that it actually
	// runs (review: doctor did not smoke-test the binary/shims).
	if info.Mode()&0o111 == 0 {
		return Check{
			Name:   "memoryctl_available",
			Status: WARN,
			Detail: local + " present but not executable — re-run `make build && ./install.sh`",
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if rerr := exec.CommandContext(ctx, local, "--help").Run(); rerr != nil {
		return Check{
			Name:   "memoryctl_available",
			Status: WARN,
			Detail: local + " present but does not run (`--help` failed) — rebuild it: " + rerr.Error(),
		}
	}
	// The subagent shims call `inject --subagent` and `close --subagent`; a
	// binary rebuilt from an older ref (make build writes the symlink target
	// in place) refuses them, and the shims swallow that — subagent memory
	// silently stops. With `{}` on stdin a current binary is a no-op for both.
	for _, verb := range []string{"close", "inject"} {
		probe := exec.CommandContext(ctx, local, verb, "--subagent")
		probe.Stdin = strings.NewReader("{}")
		if perr := probe.Run(); perr != nil {
			return Check{
				Name:   "memoryctl_available",
				Status: WARN,
				Detail: local + " predates subagent support (`" + verb + " --subagent` failed) — run `make build && ./install.sh`",
			}
		}
	}
	if _, err := exec.LookPath("memoryctl"); err != nil {
		return Check{
			Name:   "memoryctl_available",
			Status: WARN,
			Detail: local + " present but not on $PATH — shell invocations won't find it",
		}
	}
	return Check{Name: "memoryctl_available", Status: OK, Detail: local}
}
