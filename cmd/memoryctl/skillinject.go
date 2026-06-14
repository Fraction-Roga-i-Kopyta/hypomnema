package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
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
	ToolInput struct {
		Skill string `json:"skill"`
	} `json:"tool_input"`
}

type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
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

	var b strings.Builder
	fmt.Fprintf(&b, "Accumulated learnings for skill `%s` (apply alongside the skill):\n\n", skill)
	for _, s := range scored {
		f := bySlug[s.Slug]
		fmt.Fprintf(&b, "- %s\n", inject.CapBody(f.Body, inject.MaxBodyBytes))
		recordRecall(s.Slug) // reuse existing reinforcement (ref_count/effectiveness)
	}

	var out hookOutput
	out.HookSpecificOutput.HookEventName = "PostToolUse"
	out.HookSpecificOutput.AdditionalContext = b.String()
	enc, _ := json.Marshal(out)
	fmt.Println(string(enc))
	os.Exit(0)
}
