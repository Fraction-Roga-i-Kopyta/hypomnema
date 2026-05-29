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
	useful, silent := Classify(injected, names, text)
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
	useful, silent := Classify(injected, map[string]string{"a.md": "A"}, "")
	if len(useful) != 0 || len(silent) != 1 {
		t.Errorf("empty text → all silent; got useful=%v silent=%v", useful, silent)
	}
}
