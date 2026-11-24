package inject

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// subFact writes a fact with a description (the head the gate reads) and a
// body into dir.
func subFact(t *testing.T, dir, slug, typ, created, extraFM, desc, body string) {
	t.Helper()
	fm := fmt.Sprintf("---\nname: %s\ntype: %s\ncreated: %s\n%sdescription: %s\n---\n%s\n",
		slug, typ, created, extraFM, desc, body)
	if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runSub(t *testing.T, memDir, home string, in Input) Result {
	t.Helper()
	in.Event = "SubagentStart"
	in.SessionID = "s1:a1"
	if in.CWD == "" {
		in.CWD = "/tmp/proj"
	}
	in.ClaudeHome = filepath.Join(home, ".claude")
	in.MemoryDir = memDir
	in.Store = native.StoreFor(in.ClaudeHome, "/tmp/proj")
	in.Today = "2026-11-24"
	in.MaxK = 5
	in.MaxBytes = 5000
	res, err := Run(in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func injectedHas(res Result, slug string) bool {
	for _, s := range res.Injected {
		if s == slug+".md" || s == slug {
			return true
		}
	}
	return false
}

func TestRun_SubagentNoBriefInjectsNothing(t *testing.T) {
	memDir, projDir, home := setup(t)
	subFact(t, projDir, "continuity", "continuity", "2026-11-24", "", "where we left off", "doing things")
	subFact(t, projDir, "pinned-card", "project", "2026-11-24", "status: pinned\n", "project card", "card")
	os.WriteFile(filepath.Join(memDir, ".wal"), nil, 0o644)
	res := runSub(t, memDir, home, Input{Prompt: "where we left off project card"})
	if res.Markdown != "" || len(res.Injected) != 0 {
		t.Errorf("no brief must inject nothing; got %q %v", res.Markdown, res.Injected)
	}
}

// longBrief is a ~6 KB mixed-language task brief: it quotes absolute paths,
// says "Claude Code", carries a date, and names the real topic only near the
// end.
func longBrief() string {
	var b strings.Builder
	b.WriteString("Задача для субагента. Работаем в /Users/u/Development/hypomnema/internal/inject/inject.go и ")
	b.WriteString("/Users/u/Development/hypomnema/cmd/memoryctl/subagent.go. Claude Code запускает хук 2026-11-24.\n")
	for b.Len() < 5800 {
		b.WriteString("Внимательно прочитай файл, затем обнови поведение ranking function и проверь результат тестами. ")
		b.WriteString("Review the ranking behaviour carefully and report findings with details. ")
	}
	b.WriteString("\nНаконец проверь случай WAL oversized row в хранилище.\n")
	return b.String()
}

func TestRun_SubagentGateLongBriefNamesFact(t *testing.T) {
	memDir, projDir, home := setup(t)
	subFact(t, projDir, "wal-oversized-row", "mistake", "2026-01-01", "",
		"WAL oversized row truncates sidecar reprojection", "body text")
	for i := 1; i <= subagentMaxKTest+1; i++ {
		subFact(t, projDir, fmt.Sprintf("decoy-%d", i), "knowledge", "2026-11-24", "",
			"decoy claude notes",
			"Внимательно прочитай файл ranking function behaviour review carefully report findings details")
	}
	os.WriteFile(filepath.Join(memDir, ".wal"), nil, 0o644)
	brief := longBrief()
	res := runSub(t, memDir, home, Input{
		Prompt: "ranking behaviour", Brief: brief,
		ProjectRoot: "/Users/u/Development/hypomnema",
	})
	if !injectedHas(res, "wal-oversized-row") {
		t.Fatalf("wal-oversized-row must pass the gate; injected=%v", res.Injected)
	}
	for _, s := range res.Injected {
		if strings.HasPrefix(s, "decoy") {
			t.Errorf("decoy %s must not inject", s)
		}
	}
}

// subagentMaxKTest mirrors cmd's subagentMaxK (5).
const subagentMaxKTest = 5

func TestRun_SubagentGateIgnoresCommonTerms(t *testing.T) {
	memDir, projDir, home := setup(t)
	// 30 facts: "claude" and "code" are in the heads of 6 of them (floor
	// max(5, ceil(0.10*30)) = 5), so neither counts.
	for i := 1; i <= 24; i++ {
		subFact(t, projDir, fmt.Sprintf("filler-%d", i), "knowledge", "2026-11-20", "",
			fmt.Sprintf("filler topic%d", i), "x")
	}
	for i := 1; i <= 5; i++ {
		subFact(t, projDir, fmt.Sprintf("cc-%d", i), "knowledge", "2026-11-20", "",
			fmt.Sprintf("claude code thing%d", i), "x")
	}
	globalDir := filepath.Join(home, ".claude", "memory-global")
	os.MkdirAll(globalDir, 0o755)
	subFact(t, globalDir, "cc-global", "knowledge", "2026-11-24", "",
		"claude code unrelated gardening notes", "x")
	os.WriteFile(filepath.Join(memDir, ".wal"), nil, 0o644)
	res := runSub(t, memDir, home, Input{
		Prompt: "claude code", Brief: "Работаем с Claude Code в 2026 году, проверь настройки поведения.",
		ProjectRoot: "/Users/u/Development/hypomnema",
	})
	if len(res.Injected) != 0 {
		t.Errorf("common terms must not pass the gate; injected=%v", res.Injected)
	}
}

func TestRun_SubagentGateIgnoresBodyAndAgentType(t *testing.T) {
	memDir, projDir, home := setup(t)
	subFact(t, projDir, "agent-type-fact", "knowledge", "2026-11-24", "",
		"backend architect review", "x")
	subFact(t, projDir, "body-fact", "knowledge", "2026-11-24", "",
		"unrelated heading",
		"alpha bravo charlie delta echo foxtrot golf hotel india juliet")
	os.WriteFile(filepath.Join(memDir, ".wal"), nil, 0o644)
	res := runSub(t, memDir, home, Input{
		Prompt:    "backend architect alpha bravo charlie delta echo foxtrot golf hotel india juliet",
		Brief:     "alpha bravo charlie delta echo foxtrot golf hotel india juliet",
		AgentType: "backend-development:backend-architect",
	})
	if len(res.Injected) != 0 {
		t.Errorf("agent-type words in the query and body-only hits must not pass; injected=%v", res.Injected)
	}
}

func TestRun_SubagentContinuityGated(t *testing.T) {
	memDir, projDir, home := setup(t)
	subFact(t, projDir, "continuity", "continuity", "2026-11-24", "", "memboard tails card lock", "state")
	os.WriteFile(filepath.Join(memDir, ".wal"), nil, 0o644)
	root := "/Users/u/Development/hypomnema"
	cwd := root + "/.claude/worktrees/memboard-tails"
	res := runSub(t, memDir, home, Input{
		CWD: cwd, ProjectRoot: root, Prompt: "memboard tails",
		Brief: "fix the memboard tails: card lock",
	})
	if !injectedHas(res, "continuity") {
		t.Errorf("continuity named by the brief must inject; injected=%v", res.Injected)
	}
	res = runSub(t, memDir, home, Input{
		CWD: cwd, ProjectRoot: root, Prompt: "something else",
		Brief: "refactor the database migration runner",
	})
	if len(res.Injected) != 0 {
		t.Errorf("continuity must not inject on an unrelated brief; injected=%v", res.Injected)
	}
}
