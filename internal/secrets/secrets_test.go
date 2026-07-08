package secrets

import (
	"os"
	"path/filepath"
	"strings"
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

func TestScan_QuotedValues(t *testing.T) {
	// P0 from the 2026-07-08 review: Claude most often writes YAML/JSON with
	// quoted values; the gate must not be unquoted-only.
	hits := []string{
		`password: "realSecret123"`,
		`api_key = 'sk_live_abcd1234efgh'`,
		`"token": "ghp_abcdefgh1234567890abcd"`,
	}
	for _, c := range hits {
		if got := Scan(c); len(got) == 0 {
			t.Errorf("quoted secret must hit: %s", c)
		}
	}
	// The 8-char value minimum still applies inside quotes.
	if got := Scan(`password: "short"`); len(got) != 0 {
		t.Errorf("short quoted placeholder must not hit: %v", got)
	}
}

func TestScan_ValueBasedPatterns(t *testing.T) {
	hits := []string{
		"the key is AKIAIOSFODNN7EXAMPLE",                                     // AWS access key id
		"temp cred ASIAIOSFODNN7EXAMPLE",                                      // AWS STS key id
		"ghp_abcdefghijklmnop123456789012345678",                              // GitHub classic PAT
		"github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuv",               // GitHub fine-grained PAT
		"sk-ant-api03-abcdefghijklmnopqrstuv",                                 // Anthropic
		"xoxb-1234567890-abcdefghijklm",                                       // Slack bot token
		"-----BEGIN OPENSSH PRIVATE KEY-----",                                 // PEM block
		"Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J", // JWT
		"postgres://admin:s3cretpw@db.internal:5432/prod",                     // URL credentials
	}
	for _, c := range hits {
		if got := Scan(c); len(got) == 0 {
			t.Errorf("value-based secret must hit: %s", c)
		}
	}
	clean := []string{
		"we use AWS AKIA-prefixed key ids",                // prefix alone, no key body
		"the ghp_ prefix marks GitHub tokens",             // prefix alone
		"see https://docs.example.com/path?q=1",           // URL without credentials
		"eyJ looks like base64 of '{\"' but is too short", // JWT prefix alone
		"private key material must never be committed",    // prose
	}
	for _, c := range clean {
		if got := Scan(c); len(got) != 0 {
			t.Errorf("must not hit: %s → %v", c, got)
		}
	}
}

func TestScan_UnclosedFence(t *testing.T) {
	// One orphan fence marker must not disable scanning for the rest of
	// the document (review P1 #3 — a one-line gate bypass).
	unclosed := "intro\n```\nsome code\napi_key: sk_live_abcd1234efgh\n"
	if got := Scan(unclosed); len(got) == 0 {
		t.Error("secret after an unclosed fence must be scanned")
	}
	// A properly closed fence keeps its exemption.
	closed := "```\napi_key: sk_live_abcd1234efgh\n```\ntail text"
	if got := Scan(closed); len(got) != 0 {
		t.Errorf("properly fenced secret must stay ignored: %v", got)
	}
	// Closed pair earlier + orphan opener later: only the orphan region rescans.
	mixed := "```\nfenced: sk_live_hidden12345\n```\nprose\n```\npassword: realSecret123\n"
	got := Scan(mixed)
	if len(got) != 1 {
		t.Fatalf("want exactly the post-orphan secret, got %v", got)
	}
	if !strings.Contains(got[0], "password") {
		t.Errorf("hit should be the password line, got %v", got)
	}
}

func TestIgnoreMatch(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "memory-global")
	os.MkdirAll(globalDir, 0o755)
	os.WriteFile(filepath.Join(dir, ".secretsignore.default"), []byte("seeds/**\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".secretsignore"), []byte("# user\ndocs/examples/*\n!docs/examples/real.md\n"), 0o644)
	os.WriteFile(filepath.Join(globalDir, ".secretsignore"), []byte("vault-*.md\n"), 0o644)
	files := DefaultIgnoreFiles(dir, globalDir)
	if !IgnoreMatch("seeds/hazard.md", files...) {
		t.Error("seeds/** should whitelist seeds/hazard.md")
	}
	if !IgnoreMatch("docs/examples/demo.md", files...) {
		t.Error("docs/examples/* should whitelist demo.md")
	}
	if IgnoreMatch("docs/examples/real.md", files...) {
		t.Error("!docs/examples/real.md should UN-whitelist it")
	}
	if IgnoreMatch("mistakes/x.md", files...) {
		t.Error("unmatched path must not be whitelisted")
	}
	if !IgnoreMatch("vault-keys.md", files...) {
		t.Error("the global store's .secretsignore must whitelist too")
	}
}
