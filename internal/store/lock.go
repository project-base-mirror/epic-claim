package store

import (
	"fmt"
	"os"
	"syscall"
)

// Linux deployment is single-instance. An OS advisory lock is released even
// after a crash, unlike a stale marker, and protects restart reconciliation.
func lockDatabase(path string) (*os.File, error) {
	if path == ":memory:" {
		return nil, nil
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("database is already in use by another process")
	}
	return f, nil
}
