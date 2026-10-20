package installer

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSingleInstanceLockAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "installer.lock")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Lock(path)
	if !errors.Is(err, ErrBusy) {
		if second != nil {
			second()
		}
		t.Fatalf("second instance was not rejected: %v", err)
	}
	unlock()
	third, err := Lock(path)
	if err != nil {
		t.Fatalf("lock not released after exit: %v", err)
	}
	third()
}
