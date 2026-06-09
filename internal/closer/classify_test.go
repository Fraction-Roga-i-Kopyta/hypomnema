package closer

import (
	"sort"
	"testing"
)

func TestClassify(t *testing.T) {
	injected := []string{"css-layout-debugging.md", "docker-stale-cache.md", "sql-index.md"}
	names := map[string]string{
		"css-layout-debugging.md": "CSS layout debugging",
		"docker-stale-cache.md":   "Docker stale cache",
		"sql-index.md":            "SQL index",
	}
	text := "I applied css-layout-debugging here. Also the Docker stale cache rule helped."
	useful, silent := Classify(injected, names, nil, text)
	sort.Strings(useful)
	sort.Strings(silent)
	if len(useful) != 2 || useful[0] != "css-layout-debugging.md" || useful[1] != "docker-stale-cache.md" {
		t.Errorf("useful = %v, want css + docker", useful)
	}
	if len(silent) != 1 || silent[0] != "sql-index.md" {
		t.Errorf("silent = %v, want sql", silent)
	}
}

func TestClassify_EmptyText(t *testing.T) {
	injected := []string{"a.md"}
	useful, silent := Classify(injected, map[string]string{"a.md": "A"}, nil, "")
	if len(useful) != 0 || len(silent) != 1 {
		t.Errorf("empty text → all silent; got useful=%v silent=%v", useful, silent)
	}
}

// evidence: phrases mark a rule as applied even when the assistant never
// writes the slug — that is their whole point (CLAUDE.md "feedback" type).
func TestClassify_EvidencePhrases(t *testing.T) {
	injected := []string{"real-db.md", "other-rule.md"}
	names := map[string]string{"real-db.md": "Real DB", "other-rule.md": "Other rule"}
	evidence := map[string][]string{
		"real-db.md":    {"do not mock the database", "real database in tests"},
		"other-rule.md": {"unrelated phrase"},
	}
	text := "Spinning up a REAL database in tests via docker compose."
	useful, silent := Classify(injected, names, evidence, text)
	if len(useful) != 1 || useful[0] != "real-db.md" {
		t.Errorf("evidence phrase must mark the rule useful, got useful=%v", useful)
	}
	if len(silent) != 1 || silent[0] != "other-rule.md" {
		t.Errorf("non-matching rule stays silent, got silent=%v", silent)
	}
}

// Slug/name citation still counts for files that carry evidence (evidence
// widens the signal, it does not replace citation).
func TestClassify_EvidenceDoesNotDisableSlugMatch(t *testing.T) {
	injected := []string{"real-db.md"}
	names := map[string]string{"real-db.md": "Real DB"}
	evidence := map[string][]string{"real-db.md": {"never matching phrase"}}
	useful, _ := Classify(injected, names, evidence, "as real-db.md says, use postgres")
	if len(useful) != 1 {
		t.Errorf("slug citation must still count, got %v", useful)
	}
}
