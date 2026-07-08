// Package secrets is the v2 credential gate: it detects plaintext secrets in
// memory-file content (outside code blocks) and matches .secretsignore
// whitelists. Ported from hooks/memory-secrets-detect.sh.
package secrets

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// secretRe matches a credential-looking KEY = VALUE. The key is any word
// token that *contains* one of the credential words, so snake_case and
// SCREAMING_SNAKE keys (db_password, client_secret, AWS_SECRET_ACCESS_KEY)
// are covered — a `\b`-anchored keyword only matched bare/camelCase keys and
// let the canonical config/env form through (v2.5.1 review S1). Word chars on
// both sides of the credential word let the key carry arbitrary affixes; the
// trailing `[:=]` + ≥8-char value keeps prose false-positives low.
var secretRe = regexp.MustCompile(`(?i)[A-Za-z0-9_-]*(api[_-]?key|apikey|secret|password|passphrase|token)[A-Za-z0-9_-]*["'` + "`" + `]?\s*[:=]\s*["'` + "`" + `]?[^\s"'` + "`" + `]{8,}`)
var inlineCodeRe = regexp.MustCompile("`[^`]*`")

// valueRes are key-independent credential shapes: the value alone is
// sufficient evidence regardless of what (if anything) names it. Each
// pattern anchors on a vendor-fixed prefix or rigid structure so prose
// mentioning the *concept* ("the ghp_ prefix") never matches.
var valueRes = []*regexp.Regexp{
	regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`),                                     // AWS access/STS key id
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}\b`),                                  // GitHub classic tokens
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}\b`),                                // GitHub fine-grained PAT
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{16,}`),                                     // Anthropic API key
	regexp.MustCompile(`\bxox[bpars]-[A-Za-z0-9-]{10,}\b`),                                // Slack tokens
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),                              // PEM private key block
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), // JWT (header.payload.sig — payload also starts eyJ)
	regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^\s:/@]+:[^\s@/]+@`),                       // scheme://user:pass@ URL credentials (password cannot span '/', else ordinary dev URLs like http://host:port/@scope match — review R1)
}

// fenceMarker reports whether line opens/closes a CommonMark code fence
// (``` or ~~~), and returns the info-string that trails the marker run. The
// info-string is markup, not exempt code content, so it is still scanned — a
// secret parked as ```<secret> renders as an empty code block yet sits in
// plaintext in the raw file (review S2).
func fenceMarker(line string) (isFence bool, info string) {
	for _, mark := range []string{"```", "~~~"} {
		if strings.HasPrefix(line, mark) {
			return true, strings.TrimLeft(line[len(mark):], "`~")
		}
	}
	return false, ""
}

// Scan returns "line: fragment" hits for secret-looking tokens in content,
// outside fenced and inline code. An empty result means clean.
//
// Fences are resolved in a first pass so an UNCLOSED fence cannot exempt
// the remainder of the document: an orphan opener (no matching closer by
// EOF) is treated as plain text and everything after it is scanned. Only
// properly paired fences retain the code-block exemption. Fence delimiter
// lines are never exempt themselves — their info-string is scanned.
func Scan(content string) []string {
	lines := strings.Split(content, "\n")
	protected := make([]bool, len(lines))
	fenceInfo := make([]string, len(lines))
	inFence := false
	fenceStart := -1
	for i, line := range lines {
		if isFence, info := fenceMarker(line); isFence {
			if inFence {
				inFence = false
			} else {
				inFence = true
				fenceStart = i
			}
			protected[i] = true
			fenceInfo[i] = info
			continue
		}
		protected[i] = inFence
	}
	if inFence {
		// Orphan opener: un-protect it and everything after it.
		for i := fenceStart; i < len(lines); i++ {
			protected[i] = false
		}
	}

	var hits []string
	scan := func(n int, text string) {
		text = inlineCodeRe.ReplaceAllString(text, "")
		if m := secretRe.FindString(text); m != "" {
			hits = append(hits, fmt.Sprintf("%d: %s", n, m))
		}
		for _, re := range valueRes {
			if m := re.FindString(text); m != "" {
				hits = append(hits, fmt.Sprintf("%d: %s", n, m))
			}
		}
	}
	for i, line := range lines {
		n := i + 1
		if protected[i] {
			// Still scan the info-string on a fence delimiter line.
			if info := fenceInfo[i]; info != "" {
				scan(n, info)
			}
			continue
		}
		scan(n, line)
	}
	return hits
}

// IgnoreMatch reports whether relPath (relative to its memory store) is
// whitelisted by any of the given .secretsignore files. Files are checked in
// order; within a file the last matching pattern wins and `!` negates.
// Missing files simply don't match. Callers pass the legacy runtime tree's
// .secretsignore.default/.secretsignore plus the global store's
// .secretsignore (the documented user-facing location).
func IgnoreMatch(relPath string, ignoreFiles ...string) bool {
	for _, p := range ignoreFiles {
		if matchIgnoreFile(p, relPath) {
			return true
		}
	}
	return false
}

// DefaultIgnoreFiles returns the standard .secretsignore lookup chain for a
// legacy runtime dir + global store pair.
func DefaultIgnoreFiles(memoryDir, globalDir string) []string {
	return []string{
		filepath.Join(memoryDir, ".secretsignore.default"),
		filepath.Join(memoryDir, ".secretsignore"),
		filepath.Join(globalDir, ".secretsignore"),
	}
}

func matchIgnoreFile(path, rel string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	matched := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		negate := false
		if strings.HasPrefix(line, "!") {
			negate = true
			line = line[1:]
		}
		// An over-broad pattern (`*`, `**`, `**/*`) would whitelist the entire
		// store, silently disabling the secret gate for good — and an agent can
		// self-add one with a single non-secret write to `.secretsignore`
		// (review S4). Ignore such patterns; per-file/glob patterns still work.
		if isOverBroad(line) {
			continue
		}
		if globToRe(line).MatchString(rel) {
			matched = !negate
		}
	}
	return matched
}

// isOverBroad reports whether a whitelist pattern matches (nearly) every path
// in the store, which would neutralise the gate. Bare `*`, `**`, `**/*` and
// runs of only `*`/`/` qualify.
func isOverBroad(pattern string) bool {
	switch pattern {
	case "*", "**", "**/*", "*/**", "**/**":
		return true
	}
	return strings.Trim(pattern, "*/") == ""
}

// globToRe converts a gitignore-subset pattern to an anchored regexp: ** →
// any chars (incl. /), * → any chars except /, everything else literal.
func globToRe(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		if strings.HasPrefix(glob[i:], "**") {
			b.WriteString(".*")
			i++
			continue
		}
		if glob[i] == '*' {
			b.WriteString("[^/]*")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(glob[i])))
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return regexp.MustCompile(`$^`)
	}
	return re
}
