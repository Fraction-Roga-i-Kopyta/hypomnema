package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

var timeNow = time.Now

// today returns the current date as YYYY-MM-DD for recency scoring. Override
// with $HYPOMNEMA_TODAY (used by tests/replay to pin the clock).
func today() string {
	if d := os.Getenv("HYPOMNEMA_TODAY"); d != "" {
		return d
	}
	return timeNow().Format("2006-01-02")
}

// runRank: memoryctl rank --query "<words>" [--project P] [--k N]
// Diagnostic — ranks the sidecar's records against the query and prints them.
func runRank(args []string) {
	var query, project string
	k := 10
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--query":
			i++
			if i < len(args) {
				query = args[i]
			}
		case "--project":
			i++
			if i < len(args) {
				project = args[i]
			}
		case "--k":
			i++
			if i < len(args) {
				fmt.Sscanf(args[i], "%d", &k)
			}
		case "-h", "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl rank: unknown flag %q\n", args[i])
			os.Exit(2)
		}
	}

	s, err := sidecar.Open(sidecarPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "rank: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	recs, err := s.All()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rank: %v\n", err)
		os.Exit(1)
	}
	terms := tokenize.Tokenize(query, nil)
	overlap, err := s.OverlapScores(terms)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rank: %v\n", err)
		os.Exit(1)
	}

	cands := make([]rank.Candidate, 0, len(recs))
	for _, r := range recs {
		var domains []string
		if r.Domains != "" {
			domains = strings.Split(r.Domains, ",")
		}
		cands = append(cands, rank.Candidate{
			Slug:          r.Slug,
			Type:          r.Type,
			Project:       r.Project,
			Domains:       domains,
			RefCount:      r.RefCount,
			Effectiveness: r.Effectiveness,
			Status:        r.Status,
			Created:       r.Created,
			LastInjected:  r.LastInjected,
			Overlap:       overlap[r.Slug],
		})
	}

	q := rank.Query{Terms: terms, Project: project, Today: today()}
	for _, sc := range rank.Rank(q, cands, k) {
		fmt.Printf("%-40s score=%.3f overlap=%d ref=%d eff=%.2f\n",
			sc.Slug, sc.Score, sc.Overlap, sc.RefCount, sc.Effectiveness)
	}
}
