package inject

import (
	"fmt"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// A fact passes the head gate when at least two DISTINCT query terms occur
// in its head (name, description, keywords, domains); a term repeated across
// head fields counts once.
func TestGate_TwoDistinctHeadTerms(t *testing.T) {
	f := native.MemFile{Slug: "docker.md", Name: "Docker cache",
		Description: "layer cache goes stale", Keywords: []string{"docker"}, Domains: []string{"build"}}
	if !newGate("fix the docker cache", nil).pass(f) {
		t.Errorf("docker + cache in the head must pass")
	}
	if newGate("docker docker docker", nil).pass(f) {
		t.Errorf("one distinct term (repeated) must not pass")
	}
	if newGate("postgres index", nil).pass(f) {
		t.Errorf("no head hit must not pass")
	}
	kw := native.MemFile{Slug: "k.md", Name: "misc", Keywords: []string{"kubernetes"}, Domains: []string{"probes"}}
	if !newGate("kubernetes probes", nil).pass(kw) {
		t.Errorf("keywords and domains are head fields")
	}
}

// Body words never count: a long brief shares two words with almost any
// long body.
func TestGate_BodyOnlyDoesNotPass(t *testing.T) {
	f := native.MemFile{Slug: "redis.md", Name: "Redis ttl", Description: "expiry tuning",
		Body: "the docker cache layer is rebuilt when the docker cache misses"}
	if newGate("docker cache layer rebuilt", nil).pass(f) {
		t.Errorf("body-only matches must not pass the gate")
	}
	if newGate("docker redis", nil).pass(f) {
		t.Errorf("one head hit (redis) + one body hit (docker) must not pass")
	}
}

func TestGateExclusions_PathsAgentTypeDigitsCommon(t *testing.T) {
	var files []native.MemFile
	for i := 0; i < 60; i++ {
		f := native.MemFile{Slug: fmt.Sprintf("f%02d.md", i), Name: fmt.Sprintf("fact%02d", i)}
		switch {
		case i < 6:
			f.Description = fmt.Sprintf("claude topic%d", i)
		case i == 6:
			f.Description = "oversized payload diverted"
		case i == 7:
			f.Keywords = []string{"tails"}
		}
		files = append(files, f)
	}
	ex := gateExclusions("/Users/u/Development/hypomnema", "/Users/u", "backend-development:backend-architect", files)
	for _, w := range []string{"users", "development", "hypomnema", "backend", "architect", "claude"} {
		if !ex[w] {
			t.Errorf("%q must be excluded; got %v", w, ex)
		}
	}
	for _, w := range []string{"oversized", "tails", "topic0"} {
		if ex[w] {
			t.Errorf("%q must not be excluded (df below max(5, 10%%)); got %v", w, ex)
		}
	}
	g := newGate("2026 oversized payload claude hypomnema", ex)
	if g.want["2026"] {
		t.Errorf("digit-only tokens are dropped by newGate: %v", g.want)
	}
	if g.want["claude"] || g.want["hypomnema"] {
		t.Errorf("excluded terms must not be wanted: %v", g.want)
	}
	if !g.want["oversized"] || !g.want["payload"] {
		t.Errorf("ordinary terms must be wanted: %v", g.want)
	}
}

// The df floor of 5 keeps a small store from excluding every term.
func TestGateExclusions_SmallCorpusKeepsTerms(t *testing.T) {
	var files []native.MemFile
	for i := 0; i < 10; i++ {
		f := native.MemFile{Slug: fmt.Sprintf("f%d.md", i), Name: fmt.Sprintf("fact%d", i)}
		if i == 0 {
			f.Description = "alpha"
		}
		if i < 4 {
			f.Keywords = []string{"bravo"}
		}
		files = append(files, f)
	}
	ex := gateExclusions("", "", "", files)
	if ex["alpha"] || ex["bravo"] {
		t.Errorf("a 10-fact store must keep terms in fewer than 5 heads; got %v", ex)
	}
}

// Path components BELOW the project root (a worktree, a subdirectory) name
// the task itself and must keep counting.
func TestGateExclusions_WorktreeBelowRootNotExcluded(t *testing.T) {
	f := native.MemFile{Slug: "tails.md", Name: "memboard tails", Description: "card lock"}
	ex := gateExclusions("/r/hypomnema", "/Users/u", "", []native.MemFile{f})
	brief := "Work in /r/hypomnema/.claude/worktrees/memboard-tails on the memboard tails: card lock."
	if ex["memboard"] || ex["tails"] {
		t.Fatalf("components below the root must not be excluded: %v", ex)
	}
	if !newGate(brief, ex).pass(f) {
		t.Errorf("a fact named in the brief must pass")
	}
}
