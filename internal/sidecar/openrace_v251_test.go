package sidecar

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestOpen_ConcurrentSchemaCreationNoBusy(t *testing.T) { // review C3
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".sidecar.db")
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := Open(dbPath)
			if err == nil {
				s.Close()
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Errorf("concurrent Open %d failed (SQLITE_BUSY not retried?): %v", i, e)
		}
	}
}
