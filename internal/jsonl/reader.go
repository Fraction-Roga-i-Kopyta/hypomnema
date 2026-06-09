// Package jsonl streams Claude Code session-transcript JSONL files
// and extracts the assistant-authored text that evidence-learn mines.
//
// Claude Code writes one JSON object per line to
// ~/.claude/projects/<slug>/<session-uuid>.jsonl. The object type
// we care about is:
//
//	{"type":"assistant","message":{"content":[{"type":"text","text":"…"}]}}
//
// Any other type (user, attachment, hook_additional_context,
// permission-mode, deferred_tools_delta, …) is ignored. Inside the
// assistant `content` array, only items with `type == "text"` are
// extracted — `thinking` entries carry the model's hidden reasoning
// trace, which is never shown to the user and would skew evidence
// mining toward internal-only phrases.
//
// Some sessions exceed 1MB. We stream — never load the whole file
// into memory — and expose an iterator that returns one text chunk
// at a time plus the session_id attached to the first line that
// carries it.
package jsonl

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// Session pairs the session_id with the concatenated assistant text
// from its transcript, plus the headline session metrics the Stop hook
// writes into the WAL `session-metrics` event. Evidence mining needs
// ID + Text; the metrics feed self-profile calibration.
type Session struct {
	ID          string // session UUID from the `sessionId` field on record 0
	Text        string // all assistant `content[].text` joined by `\n\n`
	ToolCalls   int    // assistant `tool_use` content parts
	ToolErrors  int    // `tool_result` parts flagged is_error
	DurationSec int    // last timestamp − first timestamp, in seconds
}

// record is a minimal shape for decoding what we need from each line.
// Unknown fields are ignored by encoding/json; forward-compat free.
type record struct {
	Type      string         `json:"type"`
	SessionID string         `json:"sessionId"`
	Timestamp string         `json:"timestamp"`
	Message   *messageRecord `json:"message,omitempty"`
}

type messageRecord struct {
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	IsError bool   `json:"is_error"`
}

// ReadSession opens path and walks every line, returning one Session.
// Malformed lines are skipped (common: tool-output records that
// embed raw bytes exceeding bufio's line cap; we don't need those).
// An empty Text is not an error — the session simply had no
// assistant replies (tool-only automation).
func ReadSession(path string) (Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, err
	}
	defer f.Close()
	return decodeStream(f)
}

// decodeStream is exported to the package only for testability —
// lets tests pass strings.NewReader instead of writing temp files.
func decodeStream(r io.Reader) (Session, error) {
	sc := bufio.NewScanner(r)
	// Real transcripts routinely have multi-KB tool output lines; cap
	// at 4MB so a single large line doesn't kill the scan.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var out Session
	var b strings.Builder
	var firstTS, lastTS time.Time
	first := true
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec record
		if err := json.Unmarshal(line, &rec); err != nil {
			// Malformed line — skip, don't fail the whole session.
			continue
		}
		if first && rec.SessionID != "" {
			out.ID = rec.SessionID
			first = false
		}
		if ts, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil {
			if firstTS.IsZero() {
				firstTS = ts
			}
			lastTS = ts
		}
		if rec.Message == nil {
			continue
		}
		for _, p := range rec.Message.Content {
			switch {
			case rec.Type == "assistant" && p.Type == "tool_use":
				out.ToolCalls++
			case p.Type == "tool_result" && p.IsError:
				out.ToolErrors++
			case rec.Type == "assistant" && p.Type == "text" && p.Text != "":
				if b.Len() > 0 {
					b.WriteString("\n\n")
				}
				b.WriteString(p.Text)
			}
		}
	}
	out.Text = b.String()
	if !firstTS.IsZero() && lastTS.After(firstTS) {
		out.DurationSec = int(lastTS.Sub(firstTS).Seconds())
	}
	if err := sc.Err(); err != nil {
		// Bufio errors (oversized line etc.) are tolerated the same
		// way as malformed JSON — caller gets whatever was decoded.
		return out, nil
	}
	return out, nil
}
