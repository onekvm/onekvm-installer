package installer

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

const LockPath = "/run/lock/onekvm-installer.lock"

var ErrBusy = errors.New("another installer is running")

// Lock is shared by all commands and the recovery entry point. Keep the file
// in place: unlinking it could let another process lock a different inode.
// The kernel releases the lock on both a normal exit and a process crash.
func Lock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
