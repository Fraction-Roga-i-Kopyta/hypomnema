package inject

import (
	"sort"
	"testing"
)

func TestKeywords(t *testing.T) {
	got := Keywords("/home/user/dev/my-project", "help with docker cache layer")
	set := map[string]bool{}
	for _, k := range got {
		set[k] = true
	}
	for _, want := range []string{"docker", "cache", "layer", "help", "with"} {
		if !set[want] {
			t.Errorf("expected prompt token %q in %v", want, got)
		}
	}
	if !set["project"] {
		t.Errorf("expected basename token 'project' in %v", got)
	}
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			t.Errorf("duplicate token %q", sorted[i])
		}
	}
}

func TestKeywords_EmptyPrompt(t *testing.T) {
	got := Keywords("/tmp/alpha-svc", "")
	set := map[string]bool{}
	for _, k := range got {
		set[k] = true
	}
	if !set["alpha"] && !set["svc"] {
		t.Errorf("expected basename tokens from /tmp/alpha-svc, got %v", got)
	}
}
