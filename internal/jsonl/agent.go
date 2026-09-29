package jsonl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// agentTailBytes bounds how much of a parent transcript the subagent helpers
// scan: only the newest part can hold the Agent call that is starting right
// now or the latest user prompts, and the SubagentStart hook's cost must not
// grow with session length.
const agentTailBytes = 2 << 20

type agentRecord struct {
	Type             string `json:"type"`
	IsMeta           bool   `json:"isMeta"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	Message          *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type agentPart struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

type agentCallInput struct {
	Description  string `json:"description"`
	Prompt       string `json:"prompt"`
	SubagentType string `json:"subagent_type"`
}

// AgentCall is one Agent (or legacy Task) tool_use in a transcript.
type AgentCall struct {
	ID          string
	Description string
	Prompt      string
}

// AgentCalls scans the tail of a parent transcript for launches of
// agentType (absent/empty subagent_type counts as "general-purpose").
// pending lists the calls with no tool_result yet, oldest first. own is the
// call whose tool_result acknowledges agentID ("agentId: <id>", first match
// wins) — a background launch is acknowledged at once, often before this
// hook reads the tail; nil when no acknowledgement has landed (always for a
// synchronous launch). readable is false for an unreadable/non-regular file.
func AgentCalls(path, agentType, agentID string) (pending []AgentCall, own *AgentCall, readable bool) {
	lines, readable := readTailLines(path, agentTailBytes)
	if !readable {
		return nil, nil, false
	}
	var calls []AgentCall
	answered := map[string]bool{}
	ownID := ""
	for _, line := range lines {
		var rec agentRecord
		if json.Unmarshal(line, &rec) != nil || rec.Message == nil {
			continue
		}
		for _, p := range contentParts(rec.Message.Content) {
			switch {
			case rec.Type == "assistant" && p.Type == "tool_use" && (p.Name == "Agent" || p.Name == "Task"):
				var in agentCallInput
				if p.ID == "" || json.Unmarshal(p.Input, &in) != nil {
					continue
				}
				typ := in.SubagentType
				if typ == "" {
					typ = "general-purpose"
				}
				if typ == agentType {
					calls = append(calls, AgentCall{ID: p.ID, Description: in.Description, Prompt: in.Prompt})
				}
			case p.Type == "tool_result" && p.ToolUseID != "":
				answered[p.ToolUseID] = true
				if ownID == "" && agentID != "" && bytes.Contains(p.Content, []byte("agentId: "+agentID)) {
					ownID = p.ToolUseID
				}
			}
		}
	}
	for i := range calls {
		if ownID != "" && calls[i].ID == ownID {
			c := calls[i]
			own = &c
			break
		}
	}
	for _, c := range calls {
		if !answered[c.ID] {
			pending = append(pending, c)
		}
	}
	return pending, own, true
}

// RecentUserPrompts returns up to n of the newest human-typed prompts in the
// tail of a transcript, oldest first, newline-joined. A prompt is a user
// record that is neither meta nor a compaction summary and carries no
// tool_result; text parts that start with "<" (system reminders, task
// notifications, command wrappers) are machine text and are dropped, and a
// record with no remaining text is skipped.
func RecentUserPrompts(path string, n int) (string, bool) {
	lines, readable := readTailLines(path, agentTailBytes)
	if !readable || n <= 0 {
		return "", false
	}
	var picked []string
	for i := len(lines) - 1; i >= 0 && len(picked) < n; i-- {
		var rec agentRecord
		if json.Unmarshal(lines[i], &rec) != nil || rec.Type != "user" || rec.Message == nil ||
			rec.IsMeta || rec.IsCompactSummary {
			continue
		}
		if text, ok := humanText(rec.Message.Content); ok {
			picked = append(picked, text)
		}
	}
	if len(picked) == 0 {
		return "", false
	}
	for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
		picked[i], picked[j] = picked[j], picked[i]
	}
	return strings.Join(picked, "\n"), true
}

// humanText extracts the non-machine text of a user message content (a
// string or content parts). ok is false for tool_result records and for
// records whose every text starts with "<".
func humanText(raw json.RawMessage) (string, bool) {
	var texts []string
	var s string
	if json.Unmarshal(raw, &s) == nil {
		texts = []string{s}
	} else {
		for _, p := range contentParts(raw) {
			switch p.Type {
			case "tool_result":
				return "", false
			case "text":
				texts = append(texts, p.Text)
			}
		}
	}
	var kept []string
	for _, t := range texts {
		if t = strings.TrimSpace(t); t != "" && !strings.HasPrefix(t, "<") {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return "", false
	}
	return strings.Join(kept, "\n"), true
}

func contentParts(raw json.RawMessage) []agentPart {
	var parts []agentPart
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	return parts
}

// readTailLines returns the complete lines in the last limit bytes of path,
// oldest first. When the window starts mid-line, that partial line is
// dropped; when it starts exactly on a line boundary, the first line is
// kept. Lines longer than maxLineBytes are skipped. readable is false for
// an empty path, an unopenable file or a non-regular file.
func readTailLines(path string, limit int64) (lines [][]byte, readable bool) {
	if path == "" {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	start := int64(0)
	if info.Size() > limit {
		start = info.Size() - limit
	}
	seekTo := start
	if start > 0 {
		seekTo = start - 1 // the byte before the window tells whether it starts on a line boundary
	}
	if _, err := f.Seek(seekTo, io.SeekStart); err != nil {
		return nil, false
	}
	br := bufio.NewReader(f)
	if start > 0 {
		prev, err := br.ReadByte()
		if err != nil {
			return nil, true
		}
		if prev != '\n' {
			if _, err := br.ReadBytes('\n'); err != nil {
				return nil, true
			}
		}
	}
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 && len(line) <= maxLineBytes {
			lines = append(lines, line)
		}
		if rerr != nil {
			break
		}
	}
	return lines, true
}
