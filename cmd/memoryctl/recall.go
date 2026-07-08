package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

// defaultRecallK bounds the hybrid output: 1 rendered body + 5 index lines.
const defaultRecallK = 6

// runRecall implements `memoryctl recall <query words...> [--k N]` — the
// pull counterpart to the push injection pipeline. Interactive command:
// errors are visible, no hook fail-safe.
func runRecall(args []string) {
	k := defaultRecallK
	var words []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--k":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "memoryctl recall: --k needs a value")
				os.Exit(2)
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				fmt.Fprintf(os.Stderr, "memoryctl recall: bad --k %q\n", args[i])
				os.Exit(2)
			}
			k = n
		case "-h", "--help":
			fmt.Print(usage)
			return
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "memoryctl recall: unknown flag %q\n", args[i])
				os.Exit(2)
			}
			words = append(words, args[i])
		}
	}
	query := strings.TrimSpace(strings.Join(words, " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "memoryctl recall: empty query\nUsage: memoryctl recall <query words...> [--k N]")
		os.Exit(2)
	}

	cwd := projectCWD()
	files := native.Collect(claudeDir(), cwd)
	bySlug := make(map[string]native.MemFile, len(files))
	for _, f := range files {
		bySlug[f.Slug] = f
	}
	terms := tokenize.Relevance(query)
	cands := inject.Candidates(memoryDir(), cwd, files, terms)

	// Pull is an explicit query: a fact that matches zero terms is not an
	// answer, however fresh or effective — without this filter "no matches"
	// would never happen (recency+eff alone outrank nothing).
	matched := cands[:0]
	for _, c := range cands {
		if c.Overlap > 0 {
			matched = append(matched, c)
		}
	}

	q := rank.Query{
		Terms: terms, Project: native.SlugFromCWD(cwd),
		Today: today(), IncludeStale: true,
	}
	// Over-fetch a few: rows whose native file vanished are skipped below.
	ranked := rank.Rank(q, matched, k+4)
	kept := ranked[:0]
	for _, sc := range ranked {
		if _, ok := bySlug[sc.Slug]; !ok {
			fmt.Fprintf(os.Stderr,
				"memoryctl recall: skipping %s — native file missing (tombstones at next close)\n", sc.Slug)
			continue
		}
		kept = append(kept, sc)
	}
	if len(kept) > k {
		kept = kept[:k]
	}
	if len(kept) == 0 {
		fmt.Println("no matches")
		return
	}

	recordRecall(kept[0].Slug)
	fmt.Print(renderRecall(kept[0], kept[1:], bySlug))
}

// recordRecall logs the pull delivery to the WAL and unions the slug into
// the session's injected list, so push dedup and close classification cover
// the pull path too. Only the rendered top-1 counts as delivered.
// Session id is resolved from the environment (HYPOMNEMA_SESSION_ID →
// CLAUDE_CODE_SESSION_ID → "cli"). Callers that already hold an explicit
// session id (e.g. skill-inject reading it from stdin) should call
// recordRecallWithSession directly.
func recordRecall(slug string) {
	sid := os.Getenv("HYPOMNEMA_SESSION_ID")
	if sid == "" {
		sid = os.Getenv("CLAUDE_CODE_SESSION_ID")
	}
	recordRecallWithSession(slug, sid)
}

// recordRecallWithSession is the implementation shared by recordRecall and
// skill-inject. sid is the caller-resolved session id; if empty, WAL events
// are keyed to "cli" and no session list is updated.
func recordRecallWithSession(slug, sid string) {
	walSid := sid
	if walSid == "" {
		// `wal validate` requires a non-empty session column.
		walSid = "cli"
	}
	line := fmt.Sprintf("%s|recall|%s|%s",
		today(), wal.SanitizeField(slug), wal.SanitizeField(walSid))
	// dedupKey = the whole line: a same-day repeat recall of the same fact
	// in the same session is one delivery, not two ref_count increments.
	if err := wal.AppendStrict(memoryDir(), line, line); err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl recall: wal append failed: %v\n", err)
		return
	}
	if sid == "" {
		return // no session — nothing to dedup against, no close to classify
	}
	writeSessionList(readInjectedList(sid), []string{slug}, sid)
}

// renderRecall renders the hybrid pull output: top-1 full body (same cap as
// injection) plus an index of runner-ups with paths for follow-up reads.
func renderRecall(top rank.Scored, rest []rank.Scored, bySlug map[string]native.MemFile) string {
	var b strings.Builder
	f := bySlug[top.Slug]
	title := f.Name
	if title == "" {
		title = top.Slug
	}
	typ := top.Type
	if typ == "" {
		typ = "note"
	}
	fmt.Fprintf(&b, "## %s (%s, score %.2f)%s\n", title, typ, top.Score, staleMark(top.Status))
	if f.Body != "" {
		b.WriteString(inject.CapBody(f.Body, inject.MaxBodyBytes))
		b.WriteString("\n")
	}
	if len(rest) == 0 {
		return b.String()
	}
	b.WriteString("\nAlso relevant:\n")
	for i, sc := range rest {
		rf := bySlug[sc.Slug]
		desc := ""
		if rf.Description != "" {
			desc = " — " + rf.Description
		}
		fmt.Fprintf(&b, "%d. %s (%.2f)%s%s\n   %s\n",
			i+2, sc.Slug, sc.Score, staleMark(sc.Status), desc, rf.Path)
	}
	return b.String()
}

func staleMark(status string) string {
	if status == "stale" {
		return " [stale]"
	}
	return ""
}
