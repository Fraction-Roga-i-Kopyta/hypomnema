package inject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

const queueFactBody = "---\nname: oldfact\ntype: knowledge\ndescription: docker cache\nstatus: active\nkeywords: [docker]\n---\ndocker cache body\n"

// queueFixture writes a project fact and a rewrite task mark anchored to it.
func queueFixture(t *testing.T) (memDir, home string) {
	t.Helper()
	memDir, projDir, home := setup(t)
	if err := os.WriteFile(filepath.Join(projDir, "oldfact.md"), []byte(queueFactBody), 0o644); err != nil {
		t.Fatal(err)
	}
	st := native.StoreFor(filepath.Join(home, ".claude"), "/tmp/proj")
	writeMark(t, memDir, st.Project, "oldfact", "rewrite", native.BodySHA(queueFactBody))
	return memDir, home
}

// queueCoreFixture is queueFixture plus a project feedback fact: a core
// fact that renders at SessionStart alongside the queue line.
func queueCoreFixture(t *testing.T) (memDir, home string) {
	t.Helper()
	memDir, home = queueFixture(t)
	st := native.StoreFor(filepath.Join(home, ".claude"), "/tmp/proj")
	rule := "---\nname: lintrule\ntype: feedback\ndescription: run make lint\n---\nalways run make lint\n"
	if err := os.WriteFile(filepath.Join(st.Dir, "lintrule.md"), []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}
	return memDir, home
}

func runQueue(t *testing.T, memDir, home, event string, maxBytes int) Result {
	t.Helper()
	res, err := Run(Input{
		Event: event, Source: "startup", SessionID: "q", CWD: "/tmp/proj", Prompt: "docker cache",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 5, MaxBytes: maxBytes,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func TestRun_SessionStartQueueLine(t *testing.T) {
	memDir, home := queueCoreFixture(t)
	st := native.StoreFor(filepath.Join(home, ".claude"), "/tmp/proj")
	res := runQueue(t, memDir, home, "SessionStart", 0)
	want := "Memory queue: 1 open task(s) from /memboard — rewrite: oldfact (" + st.Project +
		"). Mention them to the user once and offer to work through them; change nothing until the user agrees. After finishing one, run: memoryctl mark <slug> done --project <project>"
	if !strings.Contains(res.Markdown, want) {
		t.Fatalf("queue line missing:\n%s\nwant:\n%s", res.Markdown, want)
	}
	if !hasSlug(res.Injected, "lintrule.md") {
		t.Fatalf("the core still renders beside the queue line: %v", res.Injected)
	}
}

func TestRun_QueueOnlyNoFacts(t *testing.T) {
	memDir, home := queueFixture(t)
	st := native.StoreFor(filepath.Join(home, ".claude"), "/tmp/proj")
	// hide the only fact so nothing renders
	writeMark(t, memDir, st.Project, "oldfact", "hide", st.Project)
	res := runQueue(t, memDir, home, "SessionStart", 0)
	if !strings.HasPrefix(res.Markdown, "# Memory Context\nMemory queue: 1 open task(s)") {
		t.Fatalf("queue-only markdown wrong: %q", res.Markdown)
	}
	if len(res.Injected) != 0 {
		t.Fatalf("no facts expected: %v", res.Injected)
	}
}

func TestRun_QueueNotOnUserPromptSubmit(t *testing.T) {
	memDir, home := queueFixture(t)
	res := runQueue(t, memDir, home, "UserPromptSubmit", 0)
	if strings.Contains(res.Markdown, "Memory queue") {
		t.Fatalf("queue must be SessionStart only:\n%s", res.Markdown)
	}
}

// The queue line's bytes are reserved before facts render: when facts and
// queue do not both fit, the queue stays and the fact gives way.
func TestRun_QueueSurvivesOverBudget(t *testing.T) {
	memDir, home := queueCoreFixture(t)
	full := runQueue(t, memDir, home, "SessionStart", 0)
	if !strings.Contains(full.Markdown, "\nMemory queue") || !hasSlug(full.Injected, "lintrule.md") {
		t.Fatalf("precondition: fact and queue line both render:\n%s", full.Markdown)
	}
	budget := len(full.Markdown) - 1
	res := runQueue(t, memDir, home, "SessionStart", budget)
	if !strings.Contains(res.Markdown, "Memory queue: 1 open task(s)") {
		t.Fatalf("queue must survive over budget:\n%s", res.Markdown)
	}
	if hasSlug(res.Injected, "lintrule.md") {
		t.Fatalf("the fact does not fit beside the reserved queue line: %v", res.Injected)
	}
	if len(res.Markdown) > budget {
		t.Fatalf("markdown %d bytes exceeds the %d budget", len(res.Markdown), budget)
	}
}
