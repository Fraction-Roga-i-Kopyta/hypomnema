package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/ab"
)

// runAB: memoryctl ab [--seed N]
// Replays the historical WAL and prints ranked-vs-random recall@K. A
// directional signal on whether ranking concentrates useful slugs — small-n
// by nature (compaction drops per-session inject linkage).
func runAB(args []string) {
	var seed int64 = 1
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--seed":
			i++
			if i < len(args) {
				fmt.Sscanf(args[i], "%d", &seed)
			}
		case "-h", "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl ab: unknown flag %q\n", args[i])
			os.Exit(2)
		}
	}

	events, err := ab.ParseWAL(filepath.Join(memoryDir(), ".wal"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ab: %v\n", err)
		os.Exit(1)
	}
	results := ab.Replay(events, []int{3, 5, 8, 12}, seed)

	fmt.Println("A/B replay — recall@K, ranked (static signals) vs random baseline")
	fmt.Println("(temporal-holdout; Overlap=0 — prompt keywords unavailable historically;")
	fmt.Println(" small-n directional signal, not a verdict)")
	for _, r := range results {
		fmt.Printf("  K=%-3d ranked=%.3f  random=%.3f  (n=%d sessions)\n",
			r.K, r.RankedRecall, r.RandomRecall, r.Sessions)
	}
}
