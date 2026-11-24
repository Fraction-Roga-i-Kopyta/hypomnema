package inject

import "testing"

func TestPullOnly(t *testing.T) {
	for value, want := range map[string]bool{
		"off":     true,
		"OFF":     true,
		" Off\n":  true,
		"":        false,
		"on":      false,
		"0":       false,
		"false":   false,
		"offline": false,
	} {
		if got := PullOnly(value); got != want {
			t.Errorf("PullOnly(%q) = %v, want %v", value, got, want)
		}
	}
	t.Setenv(PromptInjectEnv, "off")
	if !PullOnlyFromEnv() {
		t.Errorf("%s=off in the environment must select pull-only mode", PromptInjectEnv)
	}
}
