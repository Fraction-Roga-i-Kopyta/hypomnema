package evidence

import (
	"strings"
	"testing"
)

func TestMine_PhraseAppearingInAllSilentSessionsTop(t *testing.T) {
	silent := []string{
		"Let me enumerate the candidates here. Three options come to mind.",
		"Three options seem relevant — the usual candidates.",
		"Here are the candidates — three options stand out.",
	}
	noise := []string{
		"Running the build. Tests passed. No issues found.",
		"Committing the fix. PR opened.",
	}
	got := Mine(silent, noise, nil, Config{})
	if len(got) == 0 {
		t.Fatal("expected at least one candidate")
	}
	// "three options" appears in all three silent (100%) and zero
	// noise — should be top by coverage.
	found := false
	for _, c := range got {
		if c.Phrase == "three options" {
			if c.SilentSessions != 3 {
				t.Errorf("three options SilentSessions: got %d, want 3", c.SilentSessions)
			}
			if c.NoiseSessions != 0 {
				t.Errorf("three options NoiseSessions: got %d, want 0", c.NoiseSessions)
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("three options should be ranked; got %+v", got)
	}
}

func TestMine_RejectsTooGenericPhrases(t *testing.T) {
	silent := []string{
		"good morning team, here is the summary",
		"good morning team, another summary",
		"good morning team, the final one",
	}
	// Same 3-gram in every noise session too — should be rejected
	// despite 100% silent coverage.
	noise := []string{
		"good morning team — noise 1",
		"good morning team — noise 2",
		"good morning team — noise 3",
	}
	got := Mine(silent, noise, nil, Config{})
	for _, c := range got {
		if c.Phrase == "good morning team" {
			t.Errorf("'good morning team' should be filtered as noise; got %+v", c)
		}
	}
}

func TestMine_ExcludesExistingEvidence(t *testing.T) {
	silent := []string{
		"candidates to consider here",
		"candidates to consider again",
		"candidates to consider once more",
	}
	noise := []string{""}
	existing := []string{"candidates to consider"} // already on file

	got := Mine(silent, noise, existing, Config{})
	for _, c := range got {
		if c.Phrase == "candidates to consider" {
			t.Errorf("existing phrase should be excluded, got %+v", c)
		}
	}
}

func TestMine_MinRuneLenFiltersShortTokens(t *testing.T) {
	silent := []string{"a b c", "a b c", "a b c"}
	noise := []string{""}
	got := Mine(silent, noise, nil, Config{MinRuneLen: 5})
	for _, c := range got {
		if len([]rune(c.Phrase)) < 5 {
			t.Errorf("MinRuneLen=5 leaked: %+v", c)
		}
	}
}

func TestMine_CyrillicPhrasesMined(t *testing.T) {
	silent := []string{
		"мои варианты такие — первый и второй",
		"мои варианты разные — подумаем",
		"мои варианты следующие",
	}
	noise := []string{"коммит сделан"}
	got := Mine(silent, noise, nil, Config{})
	found := false
	for _, c := range got {
		if c.Phrase == "мои варианты" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'мои варианты' in candidates, got %+v", got)
	}
}

func TestMine_TopNCapApplied(t *testing.T) {
	// 20 silent sessions sharing one rare phrase each, plus one
	// phrase in common. Cap at 3; expect at most 3 back.
	silent := make([]string, 20)
	for i := range silent {
		silent[i] = "common marker observed here"
	}
	got := Mine(silent, nil, nil, Config{TopN: 3})
	if len(got) > 3 {
		t.Errorf("TopN=3 violated: got %d", len(got))
	}
}

func TestExtractPhrases_StripsCodeFences(t *testing.T) {
	text := "real prose words\n```\nfunction bodyContent() { return 42; }\n```\nmore real prose"
	phrases := extractPhrases(text, 3)
	// Code-fence tokens must not appear.
	for p := range phrases {
		if strings.Contains(p, "function bodycontent") ||
			strings.Contains(p, "bodycontent return") {
			t.Errorf("code fence leaked into phrases: %q", p)
		}
	}
	// Prose must still tokenise.
	if _, ok := phrases["real prose"]; !ok {
		t.Errorf("prose should produce 'real prose' bigram; got %v", phrases)
	}
}

func TestExtractPhrases_StripsBlockquotes(t *testing.T) {
	text := "> user asked something here\nassistant answer follows here"
	phrases := extractPhrases(text, 3)
	for p := range phrases {
		if strings.Contains(p, "user asked") {
			t.Errorf("blockquote leaked into phrases: %q", p)
		}
	}
}
