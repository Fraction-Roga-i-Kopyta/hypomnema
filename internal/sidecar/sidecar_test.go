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
