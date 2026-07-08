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

var secretRe = regexp.MustCompile(`(?i)\b(api[_-]?key|apikey|aws[_-]?(?:access|secret)[_-]?key|secret|password|token)["'` + "`" + `]?\s*[:=]\s*["'` + "`" + `]?[^\s"'` + "`" + `]{8,}`)
var inlineCodeRe = regexp.MustCompile("`[^`]*`")

// valueRes are key-independent credential shapes: the value alone is
// sufficient evidence regardless of what (if anything) names it. Each
// pattern anchors on a vendor-fixed prefix or rigid structure so prose
// mentioning the *concept* ("the ghp_ prefix") never matches.
var valueRes = []*regexp.Regexp{
	regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`),        // AWS access/STS key id
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}\b`),     // GitHub classic tokens
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}\b`),   // GitHub fine-grained PAT
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{16,}`),        // Anthropic API key
	regexp.MustCompile(`\bxox[bpars]-[A-Za-z0-9-]{10,}\b`),   // Slack tokens
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), // PEM private key block
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), // JWT (header.payload.sig — payload also starts eyJ)
	regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^\s:/@]+:[^\s@]+@`),                        // scheme://user:pass@ URL credentials
}

// Scan returns "line: fragment" hits for secret-looking tokens in content,
// outside fenced and inline code. An empty result means clean.
//
// Fences are resolved in a first pass so an UNCLOSED fence cannot exempt
// the remainder of the document: an orphan opener (no matching closer by
// EOF) is treated as plain text and everything after it is scanned. Only
// properly paired fences retain the code-block exemption.
func Scan(content string) []string {
	lines := strings.Split(content, "\n")
	protected := make([]bool, len(lines))
	inFence := false
	fenceStart := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "```") {
			if inFence {
				inFence = false
			} else {
				inFence = true
				fenceStart = i
			}
			protected[i] = true
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
	for i, line := range lines {
		if protected[i] {
			continue
		}
		n := i + 1
		line = inlineCodeRe.ReplaceAllString(line, "")
		if m := secretRe.FindString(line); m != "" {
			hits = append(hits, fmt.Sprintf("%d: %s", n, m))
		}
		for _, re := range valueRes {
			if m := re.FindString(line); m != "" {
				hits = append(hits, fmt.Sprintf("%d: %s", n, m))
			}
		}
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
		if globToRe(line).MatchString(rel) {
			matched = !negate
		}
	}
	return matched
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
