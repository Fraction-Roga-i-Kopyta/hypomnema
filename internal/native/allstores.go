package native

import (
	"os"
	"path/filepath"
	"sort"
)

// AllStores lists every native store reachable from this install: each
// <configDir>/projects/<entry>/memory directory that exists (Project = the
// entry name), the current store when it lives elsewhere (an
// autoMemoryDirectory / COWORK override), and the global store. Deduped by
// Dir, sorted by Project, global last.
func AllStores(configDir, claudeDir string, current Store) []Store {
	global := GlobalMemoryDir(filepath.Dir(claudeDir))
	seen := map[string]bool{global: true}
	var out []Store
	add := func(s Store) {
		if s.Dir == "" || seen[s.Dir] {
			return
		}
		seen[s.Dir] = true
		out = append(out, s)
	}
	entries, _ := os.ReadDir(filepath.Join(configDir, "projects"))
	for _, e := range entries {
		dir := filepath.Join(configDir, "projects", e.Name(), "memory")
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			add(Store{Dir: dir, Project: e.Name()})
		}
	}
	add(current)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return append(out, Store{Dir: global, Project: GlobalProject})
}

// StoreDirs maps each tag of AllStores(configDir, claudeDir, current) to its
// directory — the dirs argument of sidecar.ReprojectAll. On a repeated tag
// the first store wins, the one a by-tag lookup over AllStores finds. nil
// when configDir or claudeDir is unknown: nil dirs leaves the mark table as
// it is rather than judging marks against the wrong stores.
func StoreDirs(configDir, claudeDir string, current Store) map[string]string {
	if configDir == "" || claudeDir == "" {
		return nil
	}
	dirs := map[string]string{}
	for _, s := range AllStores(configDir, claudeDir, current) {
		if _, dup := dirs[s.Project]; !dup {
			dirs[s.Project] = s.Dir
		}
	}
	return dirs
}
