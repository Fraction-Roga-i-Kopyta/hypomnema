package native

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// fmBounds returns the line index of the closing fence of the frontmatter
// block in lines (lines[0] is the opening fence), or -1 when there is none.
func fmBounds(lines []string) int {
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t\r") != "---" {
		return -1
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r") == "---" {
			return i
		}
	}
	return -1
}

// statusLines returns the indices (inside lines[1:end]) of every `status:`
// key that splitFrontmatter would read: a column-0 key or a direct child of a
// column-0 `metadata:` block.
func statusLines(lines []string, end int) []int {
	var idx []int
	inMeta, metaIndent := false, -1
	for i := 1; i < end; i++ {
		ln := strings.TrimRight(lines[i], "\r")
		if strings.TrimSpace(ln) == "" {
			continue
		}
		indent := 0
		for indent < len(ln) && (ln[indent] == ' ' || ln[indent] == '\t') {
			indent++
		}
		if indent == 0 {
			inMeta, metaIndent = false, -1
		} else {
			if !inMeta {
				continue
			}
			if metaIndent < 0 {
				metaIndent = indent
			}
			if indent != metaIndent {
				continue
			}
		}
		key, _, ok := strings.Cut(ln[indent:], ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if indent == 0 && key == "metadata" {
			inMeta = true
			continue
		}
		if key == "status" {
			idx = append(idx, i)
		}
	}
	return idx
}

// SetStatus rewrites every frontmatter `status:` key (column-0 or a direct
// child of a column-0 `metadata:` block) to status, keeping indentation and
// every other byte unchanged. With no such key it inserts `status: <v>` at
// column 0 before the closing fence. Content without frontmatter is an error.
func SetStatus(content, status string) (string, error) {
	bom := ""
	if strings.HasPrefix(content, "\xef\xbb\xbf") {
		bom, content = "\xef\xbb\xbf", strings.TrimPrefix(content, "\xef\xbb\xbf")
	}
	lines := strings.Split(content, "\n")
	end := fmBounds(lines)
	if end < 0 {
		return "", errors.New("no frontmatter")
	}
	idx := statusLines(lines, end)
	if len(idx) == 0 {
		out := append([]string{}, lines[:end]...)
		out = append(out, "status: "+status)
		out = append(out, lines[end:]...)
		return bom + strings.Join(out, "\n"), nil
	}
	for _, i := range idx {
		ln := lines[i]
		indent := len(ln) - len(strings.TrimLeft(ln, " \t"))
		cr := ""
		if strings.HasSuffix(ln, "\r") {
			cr = "\r"
		}
		lines[i] = ln[:indent] + "status: " + status + cr
	}
	return bom + strings.Join(lines, "\n"), nil
}

// BodySHA is the first 12 hex chars of sha256 over content with every
// frontmatter status line removed, so pin/unpin never change the anchor.
func BodySHA(content string) string {
	lines := strings.Split(content, "\n")
	if end := fmBounds(lines); end >= 0 {
		drop := map[int]bool{}
		for _, i := range statusLines(lines, end) {
			drop[i] = true
		}
		kept := lines[:0:0]
		for i, ln := range lines {
			if !drop[i] {
				kept = append(kept, ln)
			}
		}
		content = strings.Join(kept, "\n")
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:12]
}
