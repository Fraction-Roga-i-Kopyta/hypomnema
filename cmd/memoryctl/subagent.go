package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/closer"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/jsonl"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/pathutil"
)

// subagentStdin is the SubagentStart / SubagentStop envelope subset
// memoryctl reads. session_id and transcript_path belong to the PARENT
// session; the subagent's own transcript arrives only on SubagentStop.
type subagentStdin struct {
	SessionID           string `json:"session_id"`
	CWD                 string `json:"cwd"`
	TranscriptPath      string `json:"transcript_path"`
	AgentID             string `json:"agent_id"`
	AgentType           string `json:"agent_type"`
	AgentTranscriptPath string `json:"agent_transcript_path"`
}

const (
	subagentMaxK         = 5
	subagentMaxBytes     = 5000
	subagentPromptTerms  = 40
	subagentRecentPrompt = 3
)

// defaultSubagentSkip: fork inherits the parent's context, injected memory
// included; the others are read-only lookup agents where memory is noise.
var defaultSubagentSkip = []string{"fork", "Explore", "claude-code-guide", "statusline-setup"}

// subagentSkipped reports whether agentType gets no memory. A SET
// HYPOMNEMA_SUBAGENT_SKIP (even empty) replaces the default list. An entry
// of exactly "*" matches every agent type — the full opt-out.
func subagentSkipped(agentType string) bool {
	list := defaultSubagentSkip
	if v, ok := os.LookupEnv("HYPOMNEMA_SUBAGENT_SKIP"); ok {
		list = nil
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				list = append(list, s)
			}
		}
	}
	for _, s := range list {
		if s == "*" || s == agentType {
			return true
		}
	}
	return false
}

// subagentKey is one subagent's observation unit: its own runtime lists and
// WAL session column, so its deliveries and citations never mix with the
// parent session's.
func subagentKey(sessionID, agentID string) string {
	return sessionID + ":" + agentID
}

// agentTypeTerms turns an agent type into ranking words
// ("backend-development:backend-architect" → "backend development backend
// architect"). general-purpose — the vast majority of launches — carries no
// signal and adds nothing.
func agentTypeTerms(agentType string) string {
	if agentType == "general-purpose" {
		return ""
	}
	return strings.Join(strings.FieldsFunc(agentType, func(r rune) bool {
		return r == ':' || r == '-' || r == '_'
	}), " ")
}

// subagentQuery builds the ranking query: the task this agent was assigned
// (see pickAgentCall), else the parent's recent prompts, always with the
// agent type's words. fromTask is true only on the assigned-call branch —
// the caller uses it to skip the git signal, since a query that already
// names the task doesn't need the PARENT's branch and working-tree tokens
// on top.
func subagentQuery(parentTranscript, agentType, agentID string) (query string, fromTask bool) {
	typ := agentTypeTerms(agentType)
	if c, ok := pickAgentCall(parentTranscript, agentType, agentID); ok {
		return strings.TrimSpace(c.Description + " " + typ + " " + inject.TopTerms(c.Prompt, subagentPromptTerms)), true
	}
	if p, ok := jsonl.RecentUserPrompts(parentTranscript, subagentRecentPrompt); ok {
		return strings.TrimSpace(typ + " " + inject.TopTerms(p, subagentPromptTerms)), false
	}
	return typ, false
}

// pickAgentCall assigns this subagent the call that launched it. Its own
// acknowledged call wins. Otherwise the oldest still-unanswered call of its
// type that no sibling has claimed is claimed atomically and taken, so
// parallel launches of one type — synchronous ones are never acknowledged —
// each get a distinct call, in the order their hooks fire.
func pickAgentCall(parentTranscript, agentType, agentID string) (jsonl.AgentCall, bool) {
	pending, own, ok := jsonl.AgentCalls(parentTranscript, agentType, agentID)
	if !ok {
		return jsonl.AgentCall{}, false
	}
	if own != nil {
		claimAgentCall(own.ID) // siblings without an acknowledgement skip it
		return *own, true
	}
	for _, c := range pending {
		if claimAgentCall(c.ID) {
			return c, true
		}
	}
	return jsonl.AgentCall{}, false
}

// claimAgentCall atomically marks a launching call as taken; false when a
// sibling already holds it or the runtime dir is unusable.
func claimAgentCall(id string) bool {
	dir := filepath.Join(memoryDir(), ".runtime")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	f, err := os.OpenFile(filepath.Join(dir, "agentcall-"+pathutil.SafeFileName(id)+".claim"),
		os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// claimSubagentStart atomically creates the key's rendered list as the
// "first start happened" marker. Only the creator proceeds: a resumed
// agent (the harness does not add a second SubagentStart context) or a
// concurrent duplicate start finds the file and does nothing. Any other
// error is treated the same way — fail-safe, no rows without a claim.
func claimSubagentStart(key string) bool {
	if err := os.MkdirAll(filepath.Join(memoryDir(), ".runtime"), 0o755); err != nil {
		return false
	}
	f, err := os.OpenFile(renderedListPath(key), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// runSubagentStart is `inject --event=SubagentStart`: rank the store for
// the subagent's task and hand the top facts to the subagent's context.
func runSubagentStart(raw []byte) {
	var in subagentStdin
	if json.Unmarshal(raw, &in) != nil || in.SessionID == "" || in.AgentID == "" {
		return
	}
	if subagentSkipped(in.AgentType) {
		return
	}
	key := subagentKey(in.SessionID, in.AgentID)
	if !claimSubagentStart(key) {
		// Resumed agent (the harness does not add a second SubagentStart
		// context to a transcript that already carries one) or a concurrent
		// duplicate start: someone else already claimed this key.
		return
	}
	query, fromTask := subagentQuery(in.TranscriptPath, in.AgentType, in.AgentID)
	res, err := inject.Run(inject.Input{
		Event: "SubagentStart", SessionID: key, CWD: in.CWD,
		Prompt: query, NoGitSignal: fromTask,
		ClaudeHome: claudeDir(), MemoryDir: memoryDir(), Today: today(),
		MaxK: subagentMaxK, MaxBytes: subagentMaxBytes,
		Store: resolveStore(in.CWD).Store,
		// Read-only: a fact the parent session is withholding must not reach
		// its subagent, or the subagent's output would contaminate the
		// parent's holdout observation.
		HoldoutSession: readListFile(holdoutListPath(in.SessionID)),
	})
	if err != nil {
		return
	}
	if len(res.Injected) > 0 {
		persistInjected(nil, res.Injected, res.ProjectBySlug, key)
	}
	// Written even when nothing rendered: it marks this agent's first start.
	writeRenderedList(nil, res.Injected, key)
	if strings.TrimSpace(res.Markdown) != "" {
		emitEnvelope("SubagentStart", res.Markdown)
	}
}

// runSubagentStop is `close --subagent`: classify the subagent's own
// transcript under its observation key. Session-level work stays with the
// parent's Stop.
func runSubagentStop(raw []byte) {
	var in subagentStdin
	if json.Unmarshal(raw, &in) != nil || in.SessionID == "" || in.AgentID == "" || in.AgentTranscriptPath == "" {
		return
	}
	if subagentSkipped(in.AgentType) {
		return
	}
	_, _ = closer.Run(closer.Input{
		SessionID: subagentKey(in.SessionID, in.AgentID), CWD: in.CWD,
		TranscriptPath: in.AgentTranscriptPath,
		ClaudeHome:     claudeDir(), MemoryDir: memoryDir(), Today: today(),
		Store: resolveStore(in.CWD).Store, Subagent: true,
	})
}
