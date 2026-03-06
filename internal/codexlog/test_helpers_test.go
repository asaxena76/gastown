package codexlog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func filepathJoin(t *testing.T, elems ...string) string {
	t.Helper()
	path := filepath.Join(elems...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	return path
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func fixedTime(sec int64) time.Time {
	return time.Unix(sec, 0)
}
