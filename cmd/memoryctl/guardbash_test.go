package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const guardBashMarker = `BLOCKED (hypomnema): memoryctl pin/unpin/mark are /memboard actions; the model may only run "memoryctl mark <slug> done".`

func bashEnvelope(t *testing.T, command string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]string{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGuardBash(t *testing.T) {
	cases := []struct {
		name    string
		command string
		block   bool
	}{
		// Blocked: the panel-only verbs, however they are reached.
		{"bare pin", "memoryctl pin x.md", true},
		{"installed path mark hide", "~/.claude/bin/memoryctl mark x.md hide", true},
		{"after cd &&", "cd /tmp && memoryctl unpin x.md", true},
		{"after assignment", "X=1 memoryctl pin x", true},
		{"after env", "env memoryctl pin x", true},
		{"after command", "command memoryctl pin x", true},
		{"inside sh -c", "sh -c 'memoryctl pin x'", true},
		{"inside bash -lc", `bash -lc "memoryctl mark x less"`, true},
		{"after xargs", "xargs memoryctl pin", true},
		{"command -v substitution", "$(command -v memoryctl) pin x", true},
		{"after ;", "true; memoryctl pin x", true},
		{"after ||", "false || memoryctl pin x", true},
		{"after pipe", "ls | memoryctl pin x", true},
		{"after newline", "true\nmemoryctl pin x", true},
		{"substitution inside echo", `echo "$(memoryctl pin x)"`, true},
		{"backticks inside git", "git commit -m \"`memoryctl mark x hide`\"", true},
		{"heredoc into sh", "sh <<'EOF'\nmemoryctl pin x\nEOF", true},
		{"heredoc into bash -s", "bash -s <<EOF\nmemoryctl mark x hide\nEOF", true},
		{"heredoc into /bin/sh", "/bin/sh <<EOF\nmemoryctl pin x\nEOF", true},
		{"heredoc into env bash", "env bash <<EOF\nmemoryctl pin x\nEOF", true},
		{"heredoc piped into sh", "cat <<'EOF' | sh\nmemoryctl pin x\nEOF", true},
		{"here-string is not a heredoc", "cat <<<\"x\"\nmemoryctl pin y", true},

		// Allowed: mark … done, other verbs, and text that is not a command.
		{"mark done", "memoryctl mark x.md done --project global", false},
		{"mark done flag first", "memoryctl mark --project global x.md done", false},
		{"retire", "memoryctl retire x.md", false},
		{"stats", "memoryctl stats --json --scope session", false},
		{"echo literal", `echo "memoryctl pin"`, false},
		{"grep literal", `grep -rn "memoryctl pin" docs`, false},
		{"git commit message", `git commit -m "memoryctl mark docs"`, false},
		{"commit heredoc", "git commit -m \"$(cat <<'EOF'\nfeat: memoryctl pin/unpin\nEOF\n)\"", false},
		{"file heredoc", "cat > notes.md <<'EOF'\nmemoryctl pin x\nEOF", false},
		{"pr body heredoc", "gh pr create --body \"$(cat <<'EOF'\nmemoryctl mark x hide\nEOF\n)\"", false},

		// Edges the parser must not lose.
		{"after background &", "echo hi & memoryctl pin x", true},
		{"heredoc 2>&1 piped into sh", "cat <<'EOF' 2>&1 | sh\nmemoryctl pin x\nEOF", true},
		{"heredoc into sudo -u root bash", "sudo -u root bash <<EOF\nmemoryctl pin x\nEOF", true},
		{"quoted verb", "memoryctl 'pin' x", true},
		{"process substitution", "echo <(memoryctl pin x)", true},
		{"line continuation", "memoryctl \\\npin x", true},
		{"unterminated heredoc keeps its lines", "cat <<EOF\nmemoryctl pin x", true},
		{"arithmetic shift is not a heredoc", "echo $((1<<2))\nmemoryctl unpin x", true},
		{"--project value named done", "memoryctl mark --project done x hide", true},
		{"mark without directive", "xargs memoryctl mark x", true},
		{"done then pin", "memoryctl mark x done && memoryctl pin y", true},
		{"mark done inside sh -c", "sh -c 'memoryctl mark x done'", false},
		{"spaced heredoc to a file", "cat << EOF > notes.md\nmemoryctl pin x\nEOF", false},
		{"tab-stripped heredoc", "cat > notes.md <<-EOF\n\tmemoryctl pin x\n\tEOF", false},
		{"redirect 2>&1 after text", `grep "memoryctl pin" docs 2>&1`, false},
		{"not a panel verb", "memoryctl pinboard", false},

		// R8: text piped into a shell runs; gh is a text command like git.
		{"echo piped into sh", "echo 'memoryctl pin x' | sh", true},
		{"printf piped into bash", "printf 'memoryctl pin x\\n' | bash", true},
		{"echo piped into env bash", `echo "memoryctl mark x hide" | env bash`, true},
		{"gh title mentions mark", `gh pr create --title "feat: memoryctl mark verb" --body x`, false},
		{"gh body mentions pin", `gh pr create --title t --body "adds memoryctl pin and unpin"`, false},
		{"gh body heredoc", "gh pr create --title \"x\" --body \"$(cat <<'EOF'\nmemoryctl pin x\nEOF\n)\"", false},
		{"gh title substitution", `gh pr create --title "$(memoryctl pin x)"`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runStdin(t, nil, bashEnvelope(t, tc.command), "guard-bash")
			if tc.block {
				if code != 2 {
					t.Fatalf("%q must block (exit 2), got exit %d, stderr %q", tc.command, code, stderr)
				}
				if !strings.HasPrefix(stderr, guardBashMarker) {
					t.Errorf("stderr must start with the marker, got %q", stderr)
				}
			} else if code != 0 {
				t.Fatalf("%q must pass (exit 0), got exit %d, stderr %q", tc.command, code, stderr)
			}
			if stdout != "" {
				t.Errorf("guard-bash must print nothing on stdout, got %q", stdout)
			}
		})
	}
}

func TestGuardBash_FailOpen(t *testing.T) {
	for name, stdin := range map[string]string{
		"garbage":       "not json at all",
		"empty":         "",
		"no command":    `{"tool_name":"Bash","tool_input":{}}`,
		"wrong type":    `{"tool_input":{"command":42}}`,
		"empty command": `{"tool_input":{"command":""}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, stderr, code := runStdin(t, nil, stdin, "guard-bash")
			if code != 0 {
				t.Fatalf("unreadable input must fail open (exit 0), got %d, stderr %q", code, stderr)
			}
		})
	}
}
