package inject

import "testing"

func TestShouldWipeSidecar(t *testing.T) { // review R2
	corrupt := []string{
		"sidecar.Open: schema: file is not a database (26) (SQLITE_NOTADB)",
		"sidecar.Open: schema: database disk image is malformed (11) (SQLITE_CORRUPT)",
	}
	for _, e := range corrupt {
		if !shouldWipeSidecar(errString(e)) {
			t.Errorf("corruption must trigger a wipe: %s", e)
		}
	}
	transient := []string{
		"sidecar.Open: schema: database is locked (5) (SQLITE_BUSY)",
		"sidecar.Open: sql.Open: some transient thing",
	}
	for _, e := range transient {
		if shouldWipeSidecar(errString(e)) {
			t.Errorf("transient error must NOT wipe the sidecar (data loss under concurrency): %s", e)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
