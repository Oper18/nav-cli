package db

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// ErrLocked is returned by WithLock when the lock is still held by another
// process after the wait budget elapses.
var ErrLocked = errors.New("db: another sync is already running")

// lockRetryInterval is how often WithLock retries a non-blocking flock
// attempt while waiting for the holder to release it.
const lockRetryInterval = 100 * time.Millisecond

// WithLock serialises sync runs across overlapping hook invocations using an
// flock on <repoRoot>/.nav/lock. It tries a non-blocking lock first; on
// contention it retries for up to wait before giving up with ErrLocked so a
// caller (e.g. a hook handler) can skip the sync rather than block the
// prompt indefinitely.
func WithLock(repoRoot string, wait time.Duration, fn func() error) error {
	if _, err := Dir(repoRoot); err != nil {
		return err
	}

	path := LockPath(repoRoot)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("opening lock file %s: %w", path, err)
	}
	defer f.Close()

	deadline := time.Now().Add(wait)
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return fmt.Errorf("locking %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			return ErrLocked
		}
		time.Sleep(lockRetryInterval)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)

	return fn()
}
