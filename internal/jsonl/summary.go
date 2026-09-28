package jsonl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
)

var compactMarker = []byte(`"isCompactSummary":true`)

// LastCompactSummary returns the text of the newest compaction summary in a
// Claude Code transcript: a "user" record flagged isCompactSummary whose
// message.content is a string (or, defensively, text parts). ok is false when
// the path is empty/unreadable or holds no summary. Lines longer than
// maxLineBytes are skipped like everywhere else in this package; a cheap
// byte-level prefilter keeps the scan fast on multi-MB transcripts.
func LastCompactSummary(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	br := bufio.NewReader(f)
	var last string
	found := false
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 && len(line) <= maxLineBytes && bytes.Contains(line, compactMarker) {
			if s, ok := summaryText(line); ok {
				last, found = s, true
			}
		}
		if rerr != nil {
			break
		}
	}
	return last, found
}

func summaryText(line []byte) (string, bool) {
	var rec struct {
		IsCompactSummary bool `json:"isCompactSummary"`
		Message          struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &rec) != nil || !rec.IsCompactSummary {
		return "", false
	}
	var s string
	if json.Unmarshal(rec.Message.Content, &s) == nil {
		return s, s != ""
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(rec.Message.Content, &parts) != nil {
		return "", false
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(p.Text)
		}
	}
	return b.String(), b.Len() > 0
}
