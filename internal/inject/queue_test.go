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

func runQueue(t *testing.T, memDir, home, event string, maxBytes int) Result {
	t.Helper()
	res, err := Run(Input{
		Event: event, SessionID: "q", CWD: "/tmp/proj", Prompt: "docker cache",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 5, MaxBytes: maxBytes,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func TestRun_SessionStartQueueLine(t *testing.T) {
	memDir, home := queueFixture(t)
	st := native.StoreFor(filepath.Join(home, ".claude"), "/tmp/proj")
	res := runQueue(t, memDir, home, "SessionStart", 0)
	want := "Memory queue: 1 open task(s) from /memboard — rewrite: oldfact (" + st.Project +
		"). Mention them to the user once and offer to work through them; change nothing until the user agrees. After finishing one, run: memoryctl mark <slug> done --project <project>"
	if !strings.Contains(res.Markdown, want) {
		t.Fatalf("queue line missing:\n%s\nwant:\n%s", res.Markdown, want)
	}
	if !hasSlug(res.Injected, "oldfact.md") {
		t.Fatalf("fact still injected: %v", res.Injected)
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

func TestRun_QueueDroppedWhenOverBudget(t *testing.T) {
	memDir, home := queueFixture(t)
	full := runQueue(t, memDir, home, "SessionStart", 0)
	idx := strings.Index(full.Markdown, "\nMemory queue")
	if idx < 0 {
		t.Fatalf("no queue line:\n%s", full.Markdown)
	}
	factsOnly := full.Markdown[:idx]
	res := runQueue(t, memDir, home, "SessionStart", len(factsOnly)+10)
	if strings.Contains(res.Markdown, "Memory queue") {
		t.Fatalf("queue should be dropped over budget:\n%s", res.Markdown)
	}
	if !hasSlug(res.Injected, "oldfact.md") {
		t.Fatalf("fact must survive: %v", res.Injected)
	}
}
