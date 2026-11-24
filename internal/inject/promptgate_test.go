package inject

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

func TestPromptGate_Admits(t *testing.T) {
	g := newPromptGate("kafka consumer stuck")
	cases := []struct {
		name string
		f    native.MemFile
		want bool
	}{
		{"term in name", native.MemFile{Name: "Kafka offsets"}, true},
		{"term in description", native.MemFile{Name: "x", Description: "why a consumer stalls"}, true},
		{"term in keywords", native.MemFile{Name: "x", Keywords: []string{"kafka"}}, true},
		{"term in domains", native.MemFile{Name: "x", Domains: []string{"kafka"}}, true},
		{"two distinct terms in body", native.MemFile{Name: "x", Body: "the kafka consumer group"}, true},
		{"one term in body, repeated", native.MemFile{Name: "x", Body: "kafka, kafka and more kafka"}, false},
		{"no shared term", native.MemFile{Name: "Postgres vacuum", Body: "autovacuum tuning"}, false},
	}
	for _, c := range cases {
		if got := g.admits(c.f); got != c.want {
			t.Errorf("%s: admits = %v, want %v", c.name, got, c.want)
		}
	}

	// Function words carry no relevance, so a prompt of nothing else admits
	// nothing — not even a fact that contains those very words.
	empty := newPromptGate("ну и как так?")
	if empty.admits(native.MemFile{Name: "как так", Body: "как так вышло"}) {
		t.Error("a prompt without relevance terms must admit nothing")
	}
}

func TestWordSet_FoundInCountsDistinctTerms(t *testing.T) {
	s := newWordSet([]string{"redis", "eviction", "latency"})
	f := native.MemFile{Name: "Redis notes", Keywords: []string{"redis"}, Body: "eviction policy; redis eviction again"}
	if got := s.foundIn(f); got != 2 {
		t.Errorf("foundIn = %d, want 2 (redis, eviction)", got)
	}
	if got := newWordSet(nil).foundIn(f); got != 0 {
		t.Errorf("empty set foundIn = %d, want 0", got)
	}
}

// End to end: UserPromptSubmit injects only what the prompt admits; the cwd
// basename ranks but does not admit; SessionStart is not gated (it injects
// the core, and none of these facts is core).
func TestRun_PromptGateFiltersUserPromptSubmit(t *testing.T) {
	memDir, projDir, home := setup(t)
	facts := map[string]string{
		"kafka-lag.md": "---\nname: Kafka consumer lag\ntype: mistake\n---\npartitions rebalance slowly\n",
		"retry.md":     "---\nname: Retry policy\ntype: knowledge\n---\nthe kafka consumer retries with backoff\n",
		"grafana.md":   "---\nname: Grafana panels\ntype: knowledge\n---\na dashboard for kafka throughput\n",
		"vacuum.md":    "---\nname: Postgres vacuum\ntype: knowledge\n---\nautovacuum tuning\n",
		"layout.md":    "---\nname: Proj conventions\ntype: knowledge\n---\nproj naming rules\n",
	}
	for name, body := range facts {
		if err := os.WriteFile(filepath.Join(projDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(memDir, ".wal"), nil, 0o644)

	inject := func(event, prompt string) []string {
		t.Helper()
		res, err := Run(Input{
			Event: event, SessionID: "gate", CWD: "/tmp/proj", Prompt: prompt,
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
			Today: "2026-06-01", MaxK: 8,
		})
		if err != nil {
			t.Fatalf("Run(%s, %q): %v", event, prompt, err)
		}
		got := slices.Clone(res.Injected)
		slices.Sort(got)
		return got
	}

	if got := inject("UserPromptSubmit", "kafka consumer stuck"); !slices.Equal(got, []string{"kafka-lag.md", "retry.md"}) {
		t.Errorf("want the head match and the two-term body match, got %v", got)
	}
	if got := inject("UserPromptSubmit", "ну и как так?"); len(got) != 0 {
		t.Errorf("a prompt without relevance terms must inject nothing, got %v", got)
	}
	if got := inject("SessionStart", ""); len(got) != 0 {
		t.Errorf("SessionStart injects the core only (none here), got %v", got)
	}
}

// The prompt gate scores a fact on its own file. continuity.md / notes.md
// exist in every project, and the sidecar's keyword overlap is grouped by
// slug alone — a gate fed from it let this project's continuity.md pass on
// the words of another project's continuity.md.
func TestRun_PromptGateIgnoresSameSlugInOtherProject(t *testing.T) {
	memDir, projDir, home := setup(t)
	otherDir := filepath.Join(home, ".claude", "projects", "-tmp-other", "memory")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(otherDir, "continuity.md"),
		[]byte("---\nname: continuity\ntype: continuity\n---\ndocker cache rebuild pending\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "continuity.md"),
		[]byte("---\nname: continuity\ntype: continuity\n---\npostgres index plan pending\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker cache\ntype: mistake\n---\nlayer cache stale\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	run := func(cwd, event, prompt string) Result {
		t.Helper()
		res, err := Run(Input{
			Event: event, SessionID: "s1", CWD: cwd, Prompt: prompt,
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
			Today: "2026-05-29", MaxK: 8,
		})
		if err != nil {
			t.Fatalf("Run(%s, %s): %v", cwd, event, err)
		}
		return res
	}
	// Seed the shared sidecar with the other project's rows first.
	run("/tmp/other", "SessionStart", "")

	res := run("/tmp/proj", "UserPromptSubmit", "fix the docker cache")
	if !slices.Equal(res.Injected, []string{"docker.md"}) {
		t.Errorf("only docker.md matches the prompt in this project, got %v", res.Injected)
	}
}
