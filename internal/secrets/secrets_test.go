package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan(t *testing.T) {
	if hits := Scan("api_key: sk_live_abcd1234efgh"); len(hits) == 0 {
		t.Error("expected a hit for api_key with a long value")
	}
	if hits := Scan("password = hunter2longenough"); len(hits) == 0 {
		t.Error("expected a hit for password")
	}
	if hits := Scan("password: xxx"); len(hits) != 0 {
		t.Errorf("short placeholder must not hit: %v", hits)
	}
	if hits := Scan("```\napi_key: sk_live_abcd1234efgh\n```"); len(hits) != 0 {
		t.Errorf("fenced secret must be ignored: %v", hits)
	}
	if hits := Scan("the field `api_key: sk_live_abcd1234efgh` is an example"); len(hits) != 0 {
		t.Errorf("inline-code secret must be ignored: %v", hits)
	}
	if hits := Scan("this note is about the token bucket algorithm and rate limits"); len(hits) != 0 {
		t.Errorf("'token' without a :/= value must not hit: %v", hits)
	}
}

func TestIgnoreMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".secretsignore.default"), []byte("seeds/**\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".secretsignore"), []byte("# user\ndocs/examples/*\n!docs/examples/real.md\n"), 0o644)
	if !IgnoreMatch(dir, "seeds/hazard.md") {
		t.Error("seeds/** should whitelist seeds/hazard.md")
	}
	if !IgnoreMatch(dir, "docs/examples/demo.md") {
		t.Error("docs/examples/* should whitelist demo.md")
	}
	if IgnoreMatch(dir, "docs/examples/real.md") {
		t.Error("!docs/examples/real.md should UN-whitelist it")
	}
	if IgnoreMatch(dir, "mistakes/x.md") {
		t.Error("unmatched path must not be whitelisted")
	}
}
