package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckSettings_InvalidJSONFails(t *testing.T) { // review O4
	claude, mem, cwd := newFixture(t)
	// Corrupt JSON that nonetheless contains all six shim substrings.
	broken := "{ BROKEN " +
		`hooks/v2/session-start.sh hooks/v2/user-prompt-submit.sh hooks/v2/pre-tool-write.sh ` +
		`hooks/v2/skill-learnings-inject.sh hooks/v2/skill-active.sh hooks/v2/session-stop.sh`
	os.WriteFile(filepath.Join(claude, "settings.json"), []byte(broken), 0o644)
	mustFindCheck(t, Run(claude, mem, cwd), "settings_hooks_registered", FAIL)
}

func TestCheckSettings_WrongEventFails(t *testing.T) { // review O4
	claude, mem, cwd := newFixture(t)
	// session-start.sh registered under Stop (and session-stop under SessionStart):
	// both substrings present, but the wiring is inverted.
	s := `{"hooks":{` +
		`"SessionStart":[{"hooks":[{"type":"command","command":"~/.claude/hooks/v2/session-stop.sh"}]}],` +
		`"UserPromptSubmit":[{"hooks":[{"type":"command","command":"~/.claude/hooks/v2/user-prompt-submit.sh"}]}],` +
		`"PreToolUse":[{"matcher":"Write|Edit","hooks":[{"type":"command","command":"~/.claude/hooks/v2/pre-tool-write.sh"}]},` +
		`{"matcher":"Skill","hooks":[{"type":"command","command":"~/.claude/hooks/v2/skill-active.sh"}]}],` +
		`"PostToolUse":[{"matcher":"Skill","hooks":[{"type":"command","command":"~/.claude/hooks/v2/skill-learnings-inject.sh"}]}],` +
		`"Stop":[{"hooks":[{"type":"command","command":"~/.claude/hooks/v2/session-start.sh"}]}]}}`
	os.WriteFile(filepath.Join(claude, "settings.json"), []byte(s), 0o644)
	mustFindCheck(t, Run(claude, mem, cwd), "settings_hooks_registered", FAIL)
}

func TestCheckSettings_CorrectWiringOK(t *testing.T) { // review O4 (positive)
	claude, mem, cwd := newFixture(t)
	s := `{"hooks":{` +
		`"SessionStart":[{"hooks":[{"type":"command","command":"~/.claude/hooks/v2/session-start.sh"}]}],` +
		`"UserPromptSubmit":[{"hooks":[{"type":"command","command":"~/.claude/hooks/v2/user-prompt-submit.sh"}]}],` +
		`"PreToolUse":[{"matcher":"Write|Edit","hooks":[{"type":"command","command":"~/.claude/hooks/v2/pre-tool-write.sh"}]},` +
		`{"matcher":"Skill","hooks":[{"type":"command","command":"~/.claude/hooks/v2/skill-active.sh"}]}],` +
		`"PostToolUse":[{"matcher":"Skill","hooks":[{"type":"command","command":"~/.claude/hooks/v2/skill-learnings-inject.sh"}]}],` +
		`"Stop":[{"hooks":[{"type":"command","command":"~/.claude/hooks/v2/session-stop.sh"}]}]}}`
	os.WriteFile(filepath.Join(claude, "settings.json"), []byte(s), 0o644)
	mustFindCheck(t, Run(claude, mem, cwd), "settings_hooks_registered", OK)
}

func TestCheckShimFiles_DirectoryFails(t *testing.T) { // review R3
	claude, mem, cwd := newFixture(t)
	p := filepath.Join(claude, "hooks", "v2", "session-stop.sh")
	os.Remove(p)
	os.Mkdir(p, 0o755) // a directory has exec bits — must not count as a present shim
	mustFindCheck(t, Run(claude, mem, cwd), "shim_files_present", FAIL)
}

func TestCheckMemoryctl_NonExecutableFails(t *testing.T) { // review: doctor smoke-test
	claude, mem, cwd := newFixture(t)
	// Replace the stub binary with a NON-executable text file (mirrors a
	// symlink to a text file — doctor previously reported OK on os.Stat alone).
	p := filepath.Join(claude, "bin", "memoryctl")
	os.Remove(p)
	os.WriteFile(p, []byte("not a real binary\n"), 0o644)
	c := mustFindCheck(t, Run(claude, mem, cwd), "memoryctl_available", WARN)
	// A present-but-broken binary must not read as healthy.
	if !strings.Contains(c.Detail, "not executable") && !strings.Contains(c.Detail, "does not run") {
		t.Errorf("a non-executable memoryctl should be flagged, got %q", c.Detail)
	}
}
