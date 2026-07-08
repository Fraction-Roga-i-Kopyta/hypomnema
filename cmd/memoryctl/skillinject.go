package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/pathutil"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
)

// learningsForSkill returns up to k skill-learning facts bound to `skill`,
// ranked by effectiveness+recency (no query → overlap is 0 for all).
func learningsForSkill(skill string, k int) ([]rank.Scored, map[string]native.MemFile) {
	cwd := projectCWD()
	files := native.Collect(claudeDir(), cwd)
	bySlug := make(map[string]native.MemFile, len(files))
	for _, f := range files {
		bySlug[f.Slug] = f
	}
	// Empty terms → Candidates returns every file with Overlap 0 but
	// effectiveness/refcount populated from the sidecar/WAL.
	cands := inject.Candidates(memoryDir(), cwd, files, nil)
	matched := cands[:0:0]
	for _, c := range cands {
		mf, ok := bySlug[c.Slug]
		if !ok || mf.Type != "skill-learning" || mf.Skill != skill {
			continue
		}
		matched = append(matched, c)
	}
	q := rank.Query{
		Project:      native.SlugFromCWD(cwd),
		Today:        today(),
		IncludeStale: true,
	}
	return rank.Rank(q, matched, k), bySlug
}

const skillInjectK = 5

type skillEnvelope struct {
	SessionID string `json:"session_id"`
	ToolInput struct {
		Skill string `json:"skill"`
	} `json:"tool_input"`
}

// runSkillInject implements `memoryctl skill-inject`. It reads a PostToolUse
// hook envelope from stdin, extracts the skill name, retrieves accumulated
// skill-learning facts for that skill, and prints a hookSpecificOutput JSON
// envelope with additionalContext. A recall WAL event is written for each
// delivered learning so the reinforcement path (ref_count/effectiveness)
// is updated. Fail-safe: any error or missing skill exits 0 silently.
func runSkillInject(_ []string) {
	raw, _ := io.ReadAll(os.Stdin)
	var env skillEnvelope
	_ = json.Unmarshal(raw, &env) // fail-safe: bad/empty stdin → empty skill
	skill := strings.TrimSpace(env.ToolInput.Skill)
	if skill == "" {
		os.Exit(0) // nothing to inject
	}

	scored, bySlug := learningsForSkill(skill, skillInjectK)
	if len(scored) == 0 {
		os.Exit(0) // silent: skill has no learnings yet
	}

	// Prefer the session_id carried in the stdin envelope (spec §6: env vars
	// are unreliable in hook paths). Fall back to env resolution so the verb
	// still works when invoked manually from the CLI.
	stdinSid := strings.TrimSpace(env.SessionID)

	var b strings.Builder
	fmt.Fprintf(&b, "Accumulated learnings for skill `%s` (apply alongside the skill):\n\n", skill)
	for _, s := range scored {
		f := bySlug[s.Slug]
		entry := fmt.Sprintf("- %s\n", inject.CapBody(f.Body, inject.MaxBodyBytes))
		// Honour the same total budget as injection: an oversized envelope is
		// diverted to a file the model never reads inline, so learnings past the
		// budget would silently never surface (review H2). Stop before overflow,
		// and record recall only for learnings actually delivered.
		if b.Len()+len(entry) > inject.MaxTotalBytes && b.Len() > 0 {
			break
		}
		b.WriteString(entry)
		if stdinSid != "" {
			recordRecallWithSession(s.Slug, s.Project, stdinSid)
		} else {
			recordRecall(s.Slug, s.Project) // env-resolved sid for manual CLI invocations
		}
	}

	enc, err := marshalEnvelope("PostToolUse", b.String())
	if err != nil {
		os.Exit(0)
	}
	fmt.Println(string(enc))
	os.Exit(0)
}

type activeEnvelope struct {
	SessionID string `json:"session_id"`
	ToolInput struct {
		Skill string `json:"skill"`
	} `json:"tool_input"`
}

// runSkillActive implements `memoryctl skill-active`. It reads a PreToolUse
// hook envelope from stdin, extracts the skill name and session_id, and writes
// an active-skill marker file under memoryDir/.runtime/active-skill-<sid>.
// This lets the capture path tag skill-learnings with the correct skill even
// after context compaction. Fail-safe: exits 0 on bad/empty input.
func runSkillActive(_ []string) {
	raw, _ := io.ReadAll(os.Stdin)
	var env activeEnvelope
	_ = json.Unmarshal(raw, &env)
	skill := strings.TrimSpace(env.ToolInput.Skill)
	sid := strings.TrimSpace(env.SessionID)
	if skill == "" || sid == "" {
		os.Exit(0) // fail-safe
	}
	safe := pathutil.SafeFileName(sid)
	dir := filepath.Join(memoryDir(), ".runtime")
	_ = os.MkdirAll(dir, 0o755)
	_ = pathutil.WriteFileAtomic(filepath.Join(dir, "active-skill-"+safe), []byte(skill+"\n"), 0o644)
	os.Exit(0)
}
