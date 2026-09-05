//go:build windows

package config

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockRegionOffset is the byte the lock is taken on. Windows byte-range locks are mandatory
// rather than advisory: they block reads of the locked range as well as writes. A process
// refused the lock still has to read the holder's record at the start of the file, so the
// lock is taken on a byte far past anything the file will ever contain. Locking a range
// beyond the end of a file is allowed and does not extend it.
const lockRegionOffset = 1 << 40

// tryLockFile takes an exclusive lock without waiting. It reports false, and no error, when
// another process holds the lock.
func tryLockFile(f *os.File) (bool, error) {
	overlapped := lockRegion()
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, overlapped)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION), errors.Is(err, windows.ERROR_IO_PENDING):
		return false, nil
	default:
		return false, err
	}
}

// unlockFile drops the lock. Closing the file would drop it too, and so would the process
// exiting; this only makes the moment explicit.
func unlockFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, lockRegion())
}

func lockRegion() *windows.Overlapped {
	return &windows.Overlapped{
		Offset:     uint32(lockRegionOffset & 0xffffffff),
		OffsetHigh: uint32(lockRegionOffset >> 32),
	}
}
