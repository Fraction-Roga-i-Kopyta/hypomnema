package tokenize

// DefaultStopwords is the built-in English + Russian function-word set used for
// relevance tokenization (keyword population and query building). Without it,
// overlap counted common words at full weight (3.0), so a fact stuffed with
// function words out-ranked a genuinely relevant one — keyword stuffing / the
// "body lottery" (internal-audit ranker finding). Only words that survive the
// MinTokenRunes>=3 cut are worth listing (shorter ones — "в", "на", "the"'s
// 2-letter cousins — are already dropped by length).
//
// Kept deliberately to closed-class function words (articles, conjunctions,
// prepositions, pronouns, auxiliaries). Content words are never listed — they
// carry relevance even when common in a corpus (IDF, a separate future change,
// handles corpus-common CONTENT terms).
var DefaultStopwords = func() map[string]struct{} {
	words := []string{
		// English (>=3 runes)
		"the", "and", "for", "are", "but", "not", "you", "all", "any", "can",
		"her", "was", "one", "our", "out", "has", "had", "have", "his", "him",
		"how", "its", "may", "new", "now", "old", "see", "two", "who", "did",
		"does", "done", "this", "that", "these", "those", "then", "than", "with",
		"from", "into", "onto", "over", "under", "your", "they", "them", "their",
		"there", "here", "what", "when", "where", "which", "while", "will",
		"would", "could", "should", "shall", "must", "might", "been", "being",
		"were", "such", "some", "each", "most", "more", "much", "many", "only",
		"also", "just", "very", "too", "about", "above", "after", "again",
		"before", "because", "both", "between", "during", "other", "same",
		"use", "used", "using", "get", "got", "make", "made", "let",
		// Russian (>=3 runes)
		"что", "как", "это", "этот", "эта", "эти", "того", "этого", "этой",
		"для", "когда", "если", "или", "был", "была", "было", "были", "быть",
		"есть", "надо", "может", "можно", "где", "уже", "только", "чтобы",
		"чтоб", "потому", "поэтому", "так", "также", "тоже", "его", "они",
		"она", "оно", "все", "всё", "всех", "всем", "всего", "весь", "вся",
		"кто", "чем", "который", "которая", "которые", "которых", "нет", "да",
		"без", "над", "под", "при", "про", "после", "перед", "через", "между",
		"даже", "ещё", "еще", "уже", "вот", "нас", "вас", "них", "нам", "вам",
		"им", "их", "ему", "ней", "нее", "неё", "том", "тем", "тот", "той",
		"эту", "мой", "моя", "наш", "ваш", "свой", "своя", "себя", "себе",
		"более", "менее", "очень", "почти", "потом", "затем", "теперь", "здесь",
		"туда", "сюда", "тут", "там", "будет", "будут", "быть", "нужно",
	}
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}()

// Relevance tokenizes text for ranking use: lowercased, Unicode-aware, minimum
// rune length, with DefaultStopwords removed. This is the single policy point
// for "what tokens count as relevance signal" — keyword population and query
// building both go through it so a stopword never appears on either side.
func Relevance(text string) []string {
	return Tokenize(text, DefaultStopwords)
}
