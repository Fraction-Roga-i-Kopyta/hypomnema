// Package stats aggregates WAL usage and defines the JSON contract of
// `memoryctl stats --json`. The json tags are mirrored verbatim by the
// TypeScript panel; change them only together with the panel.
package stats

import (
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// SchemaVersion is bumped on any incompatible change of the Report shape.
const SchemaVersion = 1

// Display strips the SanitizePath(home)+"-" prefix from a project tag; the
// "global" tag and tags outside home are returned unchanged.
func Display(project, home string) string {
	if home == "" {
		return project
	}
	prefix := native.SanitizePath(home) + "-"
	if strings.HasPrefix(project, prefix) && len(project) > len(prefix) {
		return project[len(prefix):]
	}
	return project
}

type FactRef struct {
	Project       string   `json:"project"`
	Display       string   `json:"display"`
	Slug          string   `json:"slug"`
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Status        string   `json:"status"`
	Description   string   `json:"description"`
	Path          string   `json:"path"`
	Created       string   `json:"created"`
	RefCount      int      `json:"ref_count"`
	Effectiveness float64  `json:"effectiveness"`
	LastInjected  string   `json:"last_injected"`
	LastUseful    string   `json:"last_useful"`
	Injected30d   int      `json:"injected_30d"`
	Useful30d     int      `json:"useful_30d"`
	Marks         []string `json:"marks"`
}

type FactKey struct {
	Project string `json:"project"`
	Slug    string `json:"slug"`
}

type FactCount struct {
	FactRef
	Injected int `json:"injected"`
	Useful   int `json:"useful"`
}

type Day struct {
	Date     string `json:"date"`
	Injected int    `json:"injected"`
	Useful   int    `json:"useful"`
	Silent   int    `json:"silent"`
}

type Triage struct {
	Noisy      []FactRef `json:"noisy"`
	Candidates []FactRef `json:"candidates"`
	Stale      []FactRef `json:"stale"`
}

type Health struct {
	OK       int      `json:"ok"`
	Warn     int      `json:"warn"`
	Fail     int      `json:"fail"`
	Problems []string `json:"problems"`
}

type SessionStats struct {
	SessionID     string      `json:"session_id"`
	Injected      int         `json:"injected"`
	InjectedFacts int         `json:"injected_facts"`
	Useful        int         `json:"useful"`
	Silent        int         `json:"silent"`
	Undelivered   int         `json:"undelivered"`
	Recalled      int         `json:"recalled"`
	UsefulShare   float64     `json:"useful_share"`
	PromptInject  bool        `json:"prompt_inject"`
	Delivered     []FactKey   `json:"delivered"`
	UsefulFacts   []FactKey   `json:"useful_facts"`
	Top           []FactCount `json:"top"`
}

type ProjectStats struct {
	Project    string         `json:"project"`
	Display    string         `json:"display"`
	Visible    int            `json:"visible"`
	Local      int            `json:"local"`
	Global     int            `json:"global"`
	ByType     map[string]int `json:"by_type"`
	Candidates int            `json:"candidates"`
	Stale      int            `json:"stale"`
	Pinned     int            `json:"pinned"`
	Days       []Day          `json:"days"`
	Health     Health         `json:"health"`
	Triage     Triage         `json:"triage"`
	Queue      []FactRef      `json:"queue"`
}

type ProjectRow struct {
	Project  string `json:"project"`
	Display  string `json:"display"`
	Facts    int    `json:"facts"`
	Useful7d int    `json:"useful_7d"`
}

type Funnel struct {
	Stored         int `json:"stored"`
	Injected7d     int `json:"injected_7d"`
	Useful7d       int `json:"useful_7d"`
	NotInjected30d int `json:"not_injected_30d"`
}

type AllStats struct {
	Stores   int            `json:"stores"`
	Facts    int            `json:"facts"`
	Funnel   Funnel         `json:"funnel"`
	Projects []ProjectRow   `json:"projects"`
	ByType   map[string]int `json:"by_type"`
	Triage   Triage         `json:"triage"`
}

type Report struct {
	SchemaVersion int           `json:"schema_version"`
	Scope         string        `json:"scope"`
	Today         string        `json:"today"`
	Project       string        `json:"project"`
	Display       string        `json:"display"` // Project in display form (Display)
	Session       *SessionStats `json:"session,omitempty"`
	ProjectStats  *ProjectStats `json:"project_stats,omitempty"`
	All           *AllStats     `json:"all,omitempty"`
	Fact          *FactCount    `json:"fact,omitempty"`
}
