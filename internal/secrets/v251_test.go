package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan_SnakeCaseKeys(t *testing.T) { // S1
	must := []string{
		"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMIK7MDENGbPxRfiCY",
		"db_password: hunter2hunter2",
		"client_secret: s3cr3tvalue123456",
		"access_token: s3cr3tvalue123456",
		"refresh_token = s3cr3tvalue123456",
		"secret_key: abcdef123456",
	}
	for _, c := range must {
		if len(Scan(c)) == 0 {
			t.Errorf("snake_case secret must hit: %s", c)
		}
	}
}

func TestScan_URLFalsePositive(t *testing.T) { // R1
	clean := []string{
		"error came from http://localhost:5173/@vite/client during HMR",
		"https://registry.npmjs.org:443/@types/node",
		"see https://example.com:8080/notify?to=admin@corp.com",
	}
	for _, c := range clean {
		if h := Scan(c); len(h) != 0 {
			t.Errorf("ordinary dev URL must NOT hit: %s -> %v", c, h)
		}
	}
	if len(Scan("postgres://admin:s3cretpw@db.internal:5432/prod")) == 0 {
		t.Error("real connection-string credentials must still hit")
	}
}

func TestScan_FenceInfoString(t *testing.T) { // S2
	if len(Scan("```ghp_abcdefghijklmnop0123\n```\n")) == 0 {
		t.Error("secret on the fence info-string line must be scanned")
	}
	if len(Scan("```api_key: hunter2hunter2\n```\n")) == 0 {
		t.Error("key:value on the fence info-string line must be scanned")
	}
	// A legitimate language tag must not trip the gate.
	if h := Scan("```go\nfmt.Println()\n```\n"); len(h) != 0 {
		t.Errorf("language tag must not hit: %v", h)
	}
	// Body inside a properly-closed fence stays exempt.
	if h := Scan("```\napi_key: hunter2hunter2\n```\n"); len(h) != 0 {
		t.Errorf("fenced body must stay ignored: %v", h)
	}
}

func TestScan_TildeFence(t *testing.T) { // self-review #6
	if h := Scan("~~~\napi_key: hunter2hunter2\n~~~\n"); len(h) != 0 {
		t.Errorf("tilde-fenced body must be ignored like backticks: %v", h)
	}
}

func TestIgnoreMatch_RejectsOverBroad(t *testing.T) { // S4
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".secretsignore"), []byte("**\n"), 0o644)
	files := []string{filepath.Join(dir, ".secretsignore")}
	if IgnoreMatch("any/path/creds.md", files...) {
		t.Error("over-broad '**' pattern must be rejected, not whitelist everything")
	}
	// A specific pattern must still work.
	os.WriteFile(filepath.Join(dir, ".secretsignore"), []byte("seeds/**\n"), 0o644)
	if !IgnoreMatch("seeds/hazard.md", files...) {
		t.Error("specific pattern must still whitelist")
	}
}
