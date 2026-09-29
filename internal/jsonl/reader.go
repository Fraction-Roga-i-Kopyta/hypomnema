// Package jsonl streams Claude Code session-transcript JSONL files
// and extracts the assistant-authored text and thinking for citation parsing.
//
// Claude Code writes one JSON object per line to
// ~/.claude/projects/<slug>/<session-uuid>.jsonl. The object type
// we care about is:
//
//	{"type":"assistant","message":{"content":[{"type":"text","text":"…"},{"type":"thinking","thinking":"…"}]}}
//
// Any other type (user, attachment, hook_additional_context,
// permission-mode, deferred_tools_delta, …) is ignored. Inside the
// assistant `content` array, Session.Text collects only `type == "text"` items
// (for evidence mining and other evidence-learn use cases), while Session.Thinking
// collects `type == "thinking"` items separately (memory citations via
// <cc-memory filenames="…"> can appear in thinking blocks). These two streams
// never mix: Text is text-only, Thinking is thinking-only. Session.ReadPaths
// collects the `input.file_path` of every `type == "tool_use"` item whose
// `name` is "Read" AND whose matching `tool_result` (found by `id` ↔
// `tool_use_id`) came back successfully — closer uses it to credit a
// citation as delivered when the fact was never injected but the model read
// the file directly. A Read whose result reported `is_error: true`, or
// whose result never appears at all (e.g. the transcript was cut mid-turn),
// is NOT delivery — its path is dropped, not merely unconfirmed.
//
// Some sessions exceed 1MB. We stream — never load the whole file
// into memory — and expose an iterator that returns one Session
// at a time with the session_id and both text and thinking content.
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
// ID + Text; the metrics feed self-profile calibration. ReadPaths feeds the
// "delivered by direct file read" citation-credit gate: a cited fact only
// earns cite-useful if it was injected this session OR the model actually
// read the file (confirmed by a successful tool_result, not just an
// attempted Read).
type Session struct {
	ID          string   // session UUID from the `sessionId` field on record 0
	Text        string   // all assistant `content[].text` joined by `\n\n`
	Thinking    string   // all assistant `content[].thinking` joined by `\n\n` (citations may appear there)
	ReadPaths   []string // file_path of every SUCCESSFULLY-COMPLETED Read tool_use — order kept, deduped, capped at maxReadPaths
	ToolCalls   int      // assistant `tool_use` content parts
	ToolErrors  int      // `tool_result` parts flagged is_error
	DurationSec int      // last timestamp − first timestamp, in seconds
}

// maxReadPaths bounds how many distinct Read tool_use calls a session
// tracks (and, in turn, how many resolved paths ReadPaths can hold) — a
// runaway or adversarial transcript must not balloon memory;
// citation-delivery matching only ever needs to check a handful of facts
// against this list.
const maxReadPaths = 1024

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
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	IsError   bool            `json:"is_error"`
	Name      string          `json:"name"`        // tool_use tool name, e.g. "Read"
	Input     json.RawMessage `json:"input"`       // tool_use input — kept raw so a shape we don't expect can't fail the whole line
	ID        string          `json:"id"`          // tool_use id — matched against a later tool_result's tool_use_id
	ToolUseID string          `json:"tool_use_id"` // tool_result's back-reference to the tool_use that produced it
}

// readToolInput is the subset of a Read tool_use's `input` we care about.
type readToolInput struct {
	FilePath string `json:"file_path"`
}

// readFilePath extracts input.file_path from a Read tool_use, tolerating a
// missing, empty, or unexpectedly-shaped `input` (e.g. not an object) — it
// returns "" rather than erroring, so one odd tool_use never drops the rest
// of the line's content or the rest of the session (same fail-open posture
// as the rest of this package).
func readFilePath(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var in readToolInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ""
	}
	return in.FilePath
}

// readState tracks every Read tool_use seen (by id → file_path, in
// first-seen order) and, separately, whether each id's tool_result came
// back successfully. delivered() resolves the two into the final ReadPaths:
// an id with no recorded outcome (ok's zero value, false) — because its
// tool_result was an error, or never appeared at all — is excluded, not
// merely unconfirmed. This is what makes "the result never arrived" and
// "the result said is_error: true" collapse to the same NOT-delivered
// outcome.
type readState struct {
	ids   []string          // tool_use ids, first-seen order, capped at maxReadPaths
	paths map[string]string // id -> file_path
	ok    map[string]bool   // id -> tool_result succeeded; absent id reads as false (undelivered)
}

func newReadState() *readState {
	return &readState{paths: map[string]string{}, ok: map[string]bool{}}
}

// recordCall registers a Read tool_use's id/path. A blank id or path (an
// odd/missing input, see readFilePath) is never delivery-matchable, so it
// is not tracked at all.
func (rs *readState) recordCall(id, path string) {
	if id == "" || path == "" {
		return
	}
	if _, exists := rs.paths[id]; exists {
		return // duplicate tool_use id — keep the first
	}
	if len(rs.ids) >= maxReadPaths {
		return
	}
	rs.ids = append(rs.ids, id)
	rs.paths[id] = path
}

// recordResult applies a tool_result's outcome to the Read call it answers,
// if any — a tool_result for a tool we never tracked (not a Read, or a Read
// whose input didn't parse) is simply ignored.
func (rs *readState) recordResult(toolUseID string, isError bool) {
	if toolUseID == "" {
		return
	}
	if _, tracked := rs.paths[toolUseID]; !tracked {
		return
	}
	rs.ok[toolUseID] = !isError
}

// delivered returns the successfully-read paths in first-seen order,
// deduped, capped at maxReadPaths.
func (rs *readState) delivered() []string {
	seen := make(map[string]bool, len(rs.ids))
	out := make([]string, 0, len(rs.ids))
	for _, id := range rs.ids {
		if !rs.ok[id] { // false or absent — both mean not delivered
			continue
		}
		p := rs.paths[id]
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) >= maxReadPaths {
			break
		}
	}
	return out
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
// maxLineBytes bounds a single line we will parse. A transcript line larger
// than this is a giant tool payload, never assistant text we mine — we skip
// its content but MUST keep scanning. bufio.Scanner could not: on a token
// past its cap it stops the whole scan, silently dropping every line after
// the big one and fabricating "silent" for facts cited later (review E3).
const maxLineBytes = 4 * 1024 * 1024

func decodeStream(r io.Reader) (Session, error) {
	br := bufio.NewReader(r)

	var out Session
	var b strings.Builder
	var thinking strings.Builder
	rs := newReadState()
	var firstTS, lastTS time.Time
	first := true
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && len(line) <= maxLineBytes {
			processLine(line, &out, &b, &thinking, rs, &firstTS, &lastTS, &first)
		}
		// A line longer than maxLineBytes is skipped (content unneeded) but the
		// loop continues to the next line — the whole point of the E3 fix.
		if err != nil {
			break // io.EOF (or a read error) — return whatever was decoded
		}
	}
	out.Text = b.String()
	out.Thinking = thinking.String()
	out.ReadPaths = rs.delivered()
	if !firstTS.IsZero() && lastTS.After(firstTS) {
		out.DurationSec = int(lastTS.Sub(firstTS).Seconds())
	}
	return out, nil
}

func processLine(line []byte, out *Session, b *strings.Builder, thinking *strings.Builder, rs *readState, firstTS, lastTS *time.Time, first *bool) {
	var rec record
	if err := json.Unmarshal(line, &rec); err != nil {
		return // malformed line — skip, don't fail the whole session
	}
	if *first && rec.SessionID != "" {
		out.ID = rec.SessionID
		*first = false
	}
	if ts, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil {
		if firstTS.IsZero() {
			*firstTS = ts
		}
		*lastTS = ts
	}
	if rec.Message == nil {
		return
	}
	for _, p := range rec.Message.Content {
		switch {
		case rec.Type == "assistant" && p.Type == "tool_use":
			out.ToolCalls++
			if p.Name == "Read" {
				rs.recordCall(p.ID, readFilePath(p.Input))
			}
		case p.Type == "tool_result":
			if p.IsError {
				out.ToolErrors++
			}
			rs.recordResult(p.ToolUseID, p.IsError)
		case rec.Type == "assistant" && p.Type == "text" && p.Text != "":
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(p.Text)
		case rec.Type == "assistant" && p.Type == "thinking" && p.Thinking != "":
			if thinking.Len() > 0 {
				thinking.WriteString("\n\n")
			}
			thinking.WriteString(p.Thinking)
		}
	}
}
