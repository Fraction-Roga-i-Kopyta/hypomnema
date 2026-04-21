# hooks/lib/evidence-extract.sh
# shellcheck shell=bash
# Evidence extraction for feedback detector (v0.7.1).
# Two pure functions; no side effects. Source from hook scripts.
#
#   evidence_from_frontmatter <path> → stdout: newline-separated phrases
#   evidence_from_body        <path> → stdout: newline-separated unique tokens (stubbed in Task 1; implemented in Task 2)

# ---------- evidence_from_frontmatter ----------
# Reads YAML array under `evidence:` key in memory frontmatter (between `---` fences).
# Supports block-style list form:
#   evidence:
#     - "phrase one"
#     - phrase two
# Output: one phrase per line, unquoted, trimmed. Empty stdout if field absent/malformed.
evidence_from_frontmatter() {
  local file="$1"
  [ -f "$file" ] || return 0
  awk '
    BEGIN { in_fm = 0; fm_closed = 0; in_evid = 0 }
    /^---[[:space:]]*$/ {
      if (in_fm == 0 && fm_closed == 0) { in_fm = 1; next }
      if (in_fm == 1) { in_fm = 0; fm_closed = 1; exit }
    }
    in_fm == 0 { next }
    /^evidence:[[:space:]]*$/ { in_evid = 1; next }
    in_evid == 1 {
      # New top-level key (no leading whitespace, contains colon) stops the block.
      if (/^[A-Za-z_][A-Za-z0-9_-]*:/) { in_evid = 0; next }
      # Match list item: "  - value" with optional quotes.
      if (match($0, /^[[:space:]]*-[[:space:]]+/)) {
        val = substr($0, RSTART + RLENGTH)
        gsub(/^"|"$/, "", val)
        gsub(/^'"'"'|'"'"'$/, "", val)
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", val)
        if (val != "") print val
      }
    }
  ' "$file" 2>/dev/null
}

# ---------- Stop-list (ru + en + markdown noise) ----------
# Lowercased. Tokens below minimum length (4) are also filtered.
_EVIDENCE_STOPWORDS="
это что как для при если или можно нужно быть очень только также
этот эта эти они она оно свои свой такой такая такие была были есть
чего чтобы потому поэтому вообще просто всегда когда даже тоже уже
никогда всегда ничего всего нельзя надо между через чем тем того
the this that with when from have will been into than then also
what where which while would could should about their there these
those such more some only many most just even much very your you
does done same here both each whether against because before after
about across around behind beyond therefore however moreover
why how note example apply reason always never must should
body text content file path memory line code block
"

# ---------- evidence_from_body ----------
# Reads body (content after closing frontmatter `---`), excluding:
#   - Lines starting with **Why:** or **How to apply:**
#   - Fenced code blocks (between triple backticks)
#   - Blockquote lines (`>`)
#   - The slug (filename stem) itself — filtered post-tokenization
# Tokenization: lowercase, perl \w{4,} with /u flag (UTF-8 aware — works for
# Cyrillic, CJK, Greek, etc.), filter stop-words.
# Output: unique tokens, one per line.
# Truncates input at 10 KB to bound work.
evidence_from_body() {
  local file="$1"
  [ -f "$file" ] || return 0
  local slug
  slug=$(basename "$file" .md | tr '[:upper:]' '[:lower:]')

  head -c 10240 "$file" 2>/dev/null | \
    SLUG="$slug" STOP="$_EVIDENCE_STOPWORDS" perl -CSD -Mutf8 -e '
      use strict; use warnings;
      my $slug = $ENV{SLUG} // "";
      my %stop = map { lc($_) => 1 } grep { length } split /\s+/, ($ENV{STOP} // "");
      $stop{$slug} = 1;
      local $/; my $c = <STDIN>;
      $c =~ s/\A---\n.*?\n---\n//s;
      $c =~ s/```.*?```//gs;
      my %seen;
      for my $line (split /\n/, $c) {
        next if $line =~ /^\s*>/;
        next if $line =~ /^\s*\*\*Why:\*\*/;
        next if $line =~ /^\s*\*\*How to apply:\*\*/;
        while ($line =~ /(\w{4,})/gu) {
          my $t = lc($1);
          next if $stop{$t};
          next if $seen{$t}++;
          print "$t\n";
        }
      }
    ' 2>/dev/null
}
