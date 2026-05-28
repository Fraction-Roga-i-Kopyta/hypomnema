// Package tokenize turns free-form text into canonical lowercase tokens,
// Unicode-aware (unicode.IsLetter/IsDigit) so every script — Cyrillic, CJK,
// Greek — participates, not just Latin. Salvaged from internal/tfidf so the
// ranker and keyword population can tokenize without depending on the
// TF-IDF scoring package (dropped in v2).
package tokenize

import (
	"bufio"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MinTokenRunes is the minimum UTF-8-rune length a token must reach to be
// kept. Compared by runes (not bytes) for consistency across scripts.
const MinTokenRunes = 3

// Tokenize lowercases and splits text on any non-letter/digit/underscore
// rune, dropping tokens shorter than MinTokenRunes and any in stopwords
// (which may be nil).
func Tokenize(text string, stopwords map[string]struct{}) []string {
	split := func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			return r
		}
		return ' '
	}
	cleaned := strings.Map(split, text)
	fields := strings.Fields(cleaned)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		w := strings.ToLower(f)
		if utf8.RuneCountInString(w) < MinTokenRunes {
			continue
		}
		if stopwords != nil {
			if _, ok := stopwords[w]; ok {
				continue
			}
		}
		out = append(out, w)
	}
	return out
}

// LoadStopwords reads one-per-line stopwords from path (blank lines skipped,
// lowercased). A missing file is not an error — returns an empty set.
func LoadStopwords(path string) (map[string]struct{}, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	defer f.Close()
	sw := map[string]struct{}{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		sw[strings.ToLower(line)] = struct{}{}
	}
	return sw, sc.Err()
}
