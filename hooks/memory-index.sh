#!/bin/bash
# TF-IDF index builder for hypomnema memory records
# Output: ~/.claude/memory/.tfidf-index (TSV: term\tfile1:score\tfile2:score...)
# Run: manually or from stop hook when .md files changed

set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
INDEX_FILE="$MEMORY_DIR/.tfidf-index"
STOPWORDS_FILE="$MEMORY_DIR/.stopwords"

# Collect all candidate files
ALL_FILES=()
for subdir in mistakes feedback knowledge strategies notes; do
  while IFS= read -r f; do
    ALL_FILES+=("$f")
  done < <(find "$MEMORY_DIR/$subdir" -name "*.md" -type f 2>/dev/null)
done

[ ${#ALL_FILES[@]} -gt 0 ] || exit 0

TOTAL_DOCS=${#ALL_FILES[@]}

# Load stopwords
STOPWORDS=""
if [ -f "$STOPWORDS_FILE" ]; then
  STOPWORDS=$(tr '\n' '|' < "$STOPWORDS_FILE" | sed 's/|$//')
fi

# NOTE: BSD awk — no multidimensional arrays. Use SUBSEP + term_files tracking.
awk -v total_docs="$TOTAL_DOCS" -v stopwords="$STOPWORDS" '
FNR == 1 {
  if (prev_file != "" && length(body) > 0) {
    process_doc(prev_file, body, prev_has_kw)
  }
  in_fm = 0; fm_end_count = 0; body = ""; past_fm = 0; prev_file = FILENAME
  prev_has_kw = 0
}
/^---$/ {
  fm_end_count++
  if (fm_end_count == 1) { in_fm = 1; next }
  if (fm_end_count == 2) { in_fm = 0; past_fm = 1; next }
}
in_fm && /^keywords:/ { prev_has_kw = 1 }
past_fm { body = body " " $0 }

function process_doc(filepath, text, has_kw,    words, n, i, w, tf, total_w, fname) {
  if (has_kw) return

  gsub(/[^a-zA-Z\300-\377]/, " ", text)
  n = split(tolower(text), words, " ")
  total_w = 0
  for (i = 1; i <= n; i++) {
    w = words[i]
    if (length(w) < 3) continue
    if (stopwords != "" && w ~ "^(" stopwords ")$") continue
    tf[w]++
    total_w++
  }
  if (total_w == 0) return

  fname = filepath
  sub(/.*\//, "", fname)
  sub(/\.md$/, "", fname)
  for (w in tf) {
    term_tf[w, fname] = tf[w] / total_w
    doc_freq[w]++
    term_files[w] = term_files[w] " " fname
  }
}

END {
  if (prev_file != "" && length(body) > 0) process_doc(prev_file, body, prev_has_kw)

  for (term in doc_freq) {
    idf = log(total_docs / doc_freq[term]) / log(2)
    if (idf < 0.1) continue
    printf "%s", term
    n = split(term_files[term], fnames, " ")
    for (i = 1; i <= n; i++) {
      if (fnames[i] == "") continue
      tfidf = term_tf[term, fnames[i]] * idf
      printf "\t%s:%.4f", fnames[i], tfidf
    }
    printf "\n"
  }
}
' "${ALL_FILES[@]}" | sort > "$INDEX_FILE.tmp" 2>/dev/null

mv "$INDEX_FILE.tmp" "$INDEX_FILE" 2>/dev/null || true
