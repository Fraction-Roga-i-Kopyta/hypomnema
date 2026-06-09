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

var secretRe = regexp.MustCompile(`(?i)\b(api[_-]?key|apikey|aws[_-]?(?:access|secret)[_-]?key|secret|password|token)\s*[:=]\s*[^\s"'` + "`" + `]{8,}`)
var inlineCodeRe = regexp.MustCompile("`[^`]*`")

// Scan returns "line: fragment" hits for secret-looking tokens in content,
// outside fenced and inline code. An empty result means clean.
func Scan(content string) []string {
	var hits []string
	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	inFence := false
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		line = inlineCodeRe.ReplaceAllString(line, "")
		if m := secretRe.FindString(line); m != "" {
			hits = append(hits, fmt.Sprintf("%d: %s", n, m))
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
