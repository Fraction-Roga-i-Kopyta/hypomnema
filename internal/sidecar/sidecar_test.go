package sidecar

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchemaAndCloses(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sub", ".sidecar.db")
	s, err := Open(dbPath) // also creates the parent "sub" dir
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, _, err := s.Get("nonexistent.md"); err != nil {
		t.Fatalf("Get on empty table: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), ".sidecar.db")
	s1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	s2.Close()
}

func TestUpsertInsertsAndUpdates(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".sidecar.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	rec := Record{Slug: "m.md", Type: "mistake", Name: "M", RefCount: 3,
		Status: "active", Effectiveness: 0.8, ContentSHA: "sha1"}
	if err := s.Upsert(rec); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, ok, err := s.Get("m.md")
	if err != nil || !ok {
		t.Fatalf("Get after insert: ok=%v err=%v", ok, err)
	}
	if got.RefCount != 3 || got.Type != "mistake" || got.Effectiveness != 0.8 {
		t.Errorf("inserted record wrong: %+v", got)
	}

	rec.RefCount = 5
	rec.ContentSHA = "sha2"
	if err := s.Upsert(rec); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _, _ = s.Get("m.md")
	if got.RefCount != 5 || got.ContentSHA != "sha2" {
		t.Errorf("updated record wrong: %+v", got)
	}

	all, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("All len = %d, want 1 (upsert must not duplicate)", len(all))
	}
}
