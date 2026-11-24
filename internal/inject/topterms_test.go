package inject

import "testing"

func TestTopTerms_FrequencyThenFirstOccurrence(t *testing.T) {
	got := TopTerms("gamma alpha beta alpha gamma alpha delta", 2)
	if got != "alpha gamma" {
		t.Fatalf("TopTerms = %q, want %q", got, "alpha gamma")
	}
	if all := TopTerms("gamma alpha beta", -1); all != "gamma alpha beta" {
		t.Fatalf("TopTerms(n<0) = %q, want every term in first-occurrence order", all)
	}
	if empty := TopTerms("", 5); empty != "" {
		t.Fatalf("TopTerms(\"\") = %q, want empty", empty)
	}
}
