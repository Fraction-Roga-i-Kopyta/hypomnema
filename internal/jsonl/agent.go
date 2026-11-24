package jsonl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
)

// agentTailBytes bounds how much of a parent transcript AgentCalls scans:
// only the newest part can hold the Agent call that is starting right now,
// and the SubagentStart hook's cost must not grow with session length.
const agentTailBytes = 2 << 20

type agentRecord struct {
	Type    string `json:"type"`
	Message *struct {
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
