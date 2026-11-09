package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// guardBashBlocked is the stderr line guard-bash blocks with. The shim
// (hooks/v2/pre-tool-bash.sh) blocks only on its "BLOCKED (hypomnema)"
// prefix, so any other failure of the binary is a pass.
const guardBashBlocked = `BLOCKED (hypomnema): memoryctl pin/unpin/mark are /memboard actions; the model may only run "memoryctl mark <slug> done".`

// runGuardBash: memoryctl guard-bash — PreToolUse(Bash). Blocks (exit 2) a
// Bash call that runs a /memboard panel verb: pin, unpin, or mark with any
// directive but done. The panel calls these through its own process API, not
// the model's Bash tool. This stops accidental or injected CLI calls; it is
// not a security boundary — the model can still append to the WAL or edit a
// file directly. Fail-open: anything uncertain → exit 0.
func runGuardBash(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Print(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "memoryctl guard-bash: unknown flag %q\n", a)
		os.Exit(2)
	}
	defer func() {
		if recover() != nil {
			os.Exit(0) // a detector bug must never block a Bash call
		}
	}()
	raw, _ := io.ReadAll(os.Stdin)
	var in struct {
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(raw, &in) != nil {
		os.Exit(0)
	}
	if bashRunsPanelVerb(in.ToolInput.Command) {
		fmt.Fprintln(os.Stderr, guardBashBlocked)
		os.Exit(2)
	}
}

// bashRunsPanelVerb reports whether a Bash command line runs memoryctl pin,
// unpin, or mark <slug> <directive> with a directive other than done.
//
// Heredoc bodies are cut first (commit messages, PR bodies and file contents
// are not commands) unless the body is a script fed to a shell. The rest is
// split into segments on ; && || | & and newline; a segment led by grep, rg,
// echo, printf or git is text unless it holds a substitution. Every other
// segment is searched for the verb anywhere — after env/command/xargs, inside
// sh -c '…', after $(command -v memoryctl).
func bashRunsPanelVerb(cmd string) bool {
	cmd = strings.ReplaceAll(cmd, "\\\n", " ") // line continuation
	for _, s := range splitBashSegments(cutHeredocs(cmd)) {
		if textOnlySegment(s.text) {
			continue
		}
		if segmentRunsPanelVerb(s.text) {
			return true
		}
	}
	return false
}

// bashSegment is one simple command of a command line; pipedFrom marks a
// segment whose stdin is the previous segment's stdout (a single |).
type bashSegment struct {
	text      string
	start     int
	pipedFrom bool
}

// splitBashSegments splits s on ; && || | & and newline. Quotes are not
// tracked: a separator inside a quoted string splits too, which can only
// expose more text to the check, never hide a command. An & that belongs to
// a redirection (2>&1, >&2, <&0, &>file) is not a separator.
func splitBashSegments(s string) []bashSegment {
	var segs []bashSegment
	start, piped := 0, false
	cut := func(end, next int, nextPiped bool) {
		segs = append(segs, bashSegment{text: s[start:end], start: start, pipedFrom: piped})
		start, piped = next, nextPiped
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case ';', '\n':
			cut(i, i+1, false)
		case '|':
			switch {
			case i+1 < len(s) && s[i+1] == '|':
				cut(i, i+2, false)
				i++
			case i+1 < len(s) && s[i+1] == '&': // |& pipes stderr too
				cut(i, i+2, true)
				i++
			default:
				cut(i, i+1, true)
			}
		case '&':
			switch {
			case i+1 < len(s) && s[i+1] == '&':
				cut(i, i+2, false)
				i++
			case i > 0 && (s[i-1] == '>' || s[i-1] == '<'), i+1 < len(s) && s[i+1] == '>':
				// redirection, not a separator
			default:
				cut(i, i+1, false)
			}
		}
	}
	segs = append(segs, bashSegment{text: s[start:], start: start, pipedFrom: piped})
	return segs
}

// heredoc is one << operator on a line: its byte offset, delimiter word, and
// whether it is the tab-stripping <<- form.
type heredoc struct {
	op   int
	word string
	dash bool
}

func isHeredocWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

// findHeredocs lists the << operators on one line: <<WORD, <<-WORD,
// <<'WORD', <<"WORD", <<\WORD. A here-string (<<<) is not a heredoc.
func findHeredocs(line string) []heredoc {
	var hs []heredoc
	for i := 0; i+1 < len(line); i++ {
		if line[i] != '<' || line[i+1] != '<' {
			continue
		}
		j := i + 2
		if j < len(line) && line[j] == '<' {
			i = j // here-string: skip all three
			continue
		}
		dash := false
		if j < len(line) && line[j] == '-' {
			dash = true
			j++
		}
		for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
			j++
		}
		if j < len(line) && line[j] == '\\' {
			j++
		}
		var quote byte
		if j < len(line) && (line[j] == '\'' || line[j] == '"') {
			quote = line[j]
			j++
		}
		k := j
		for k < len(line) && isHeredocWordByte(line[k]) {
			k++
		}
		if k == j || quote != 0 && (k >= len(line) || line[k] != quote) {
			continue
		}
		hs = append(hs, heredoc{op: i, word: line[j:k], dash: dash})
		i = k - 1
	}
	return hs
}

// cutHeredocs drops heredoc bodies (and their delimiter lines) from cmd,
// except a body fed to a shell, which stays as script lines. A heredoc whose
// delimiter line never comes is left untouched — it may not be a heredoc at
// all ($((1<<2))), and keeping text can only make the check stricter.
func cutHeredocs(cmd string) string {
	lines := strings.Split(cmd, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		line := lines[i]
		out = append(out, line)
		i++
		for _, h := range findHeredocs(line) {
			end := -1
			for j := i; j < len(lines); j++ {
				l := lines[j]
				if h.dash {
					l = strings.TrimLeft(l, "\t")
				}
				if l == h.word {
					end = j
					break
				}
			}
			if end < 0 {
				continue
			}
			if heredocIsScript(line, h.op) {
				out = append(out, lines[i:end]...)
			}
			i = end + 1
		}
	}
	return strings.Join(out, "\n")
}

// heredocIsScript reports whether the heredoc at byte op of line feeds a
// shell: the segment holding the << runs one, or a segment piped from it does.
func heredocIsScript(line string, op int) bool {
	segs := splitBashSegments(line)
	for k, s := range segs {
		if op < s.start || op >= s.start+len(s.text) {
			continue
		}
		if runsInterpreter(s.text) {
			return true
		}
		for m := k + 1; m < len(segs) && segs[m].pipedFrom; m++ {
			if runsInterpreter(segs[m].text) {
				return true
			}
		}
		return false
	}
	return false
}

var (
	shellInterpreters = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}
	// commandWrappers run their argument as the command; the value lists the
	// flags that take a separate argument, so "sudo -u root bash" finds bash.
	commandWrappers = map[string]map[string]bool{
		"env":     {"-u": true, "-C": true, "-P": true, "--unset": true, "--chdir": true},
		"command": {},
		"exec":    {"-a": true},
		"sudo":    {"-u": true, "-g": true, "-U": true, "-C": true, "-D": true, "-h": true, "-p": true, "-r": true, "-t": true, "-T": true, "--user": true, "--group": true, "--chdir": true},
	}
	textCommands = map[string]bool{"grep": true, "rg": true, "echo": true, "printf": true, "git": true}
	reAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	reRedirect   = regexp.MustCompile(`^[0-9]*[<>]`)
)

// runsInterpreter reports whether a segment's command is a shell, looking
// past VAR=value assignments, redirections and the wrappers env, command,
// exec and sudo (with their flags).
func runsInterpreter(seg string) bool {
	words := strings.Fields(seg)
	for i := 0; i < len(words); {
		if reAssignment.MatchString(words[i]) || reRedirect.MatchString(words[i]) {
			i++
			continue
		}
		w := filepath.Base(strings.Trim(strings.TrimLeft(words[i], "({"), `'"`))
		flags, wrapper := commandWrappers[w]
		if !wrapper {
			return shellInterpreters[w]
		}
		for i++; i < len(words) && strings.HasPrefix(words[i], "-"); i++ {
			if flags[words[i]] {
				i++
			}
		}
	}
	return false
}

// textOnlySegment reports a segment that only prints or searches text: its
// first word (after VAR=value) is grep, rg, echo, printf or git, and it holds
// no command or process substitution, whose contents would run.
func textOnlySegment(seg string) bool {
	for _, sub := range []string{"$(", "`", "<(", ">("} {
		if strings.Contains(seg, sub) {
			return false
		}
	}
	words := strings.Fields(seg)
	i := 0
	for i < len(words) && reAssignment.MatchString(words[i]) {
		i++
	}
	return i < len(words) && textCommands[words[i]]
}

// reMemoryctlVerb finds memoryctl (bare or any path ending in /memoryctl,
// optionally closed by ) ' " or `) followed by a panel verb.
var reMemoryctlVerb = regexp.MustCompile("(?:^|[^A-Za-z0-9_.-])memoryctl[)'\"`]*\\s+['\"]?(pin|unpin|mark)\\b")

func segmentRunsPanelVerb(seg string) bool {
	for _, m := range reMemoryctlVerb.FindAllStringSubmatchIndex(seg, -1) {
		if seg[m[2]:m[3]] != "mark" || !markDirectiveIsDone(seg[m[1]:]) {
			return true
		}
	}
	return false
}

// markDirectiveIsDone parses what follows "memoryctl mark" the way runMark
// does (--project takes a value, other flags none) and reports whether the
// directive — the second positional — is done.
func markDirectiveIsDone(rest string) bool {
	var pos []string
	words := strings.Fields(rest)
	for i := 0; i < len(words); i++ {
		w := strings.Trim(words[i], "'\"`()")
		switch {
		case w == "--project":
			i++
		case w == "", strings.HasPrefix(w, "-"):
		default:
			pos = append(pos, w)
		}
	}
	return len(pos) >= 2 && pos[1] == "done"
}
