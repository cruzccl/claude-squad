package config

import (
	"claude-squad/log"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StateLockFileName is the file whose lock confers the right to write the saved state.
const StateLockFileName = "state.lock"

// StateLock is the exclusive right to write the saved state. One cs process holds it for as
// long as it runs, which is what keeps two of them from each rewriting the whole instance
// list from a snapshot taken at its own startup. The lock is an OS advisory lock, so the
// kernel drops it when the process dies: a crash leaves no stale lock behind and there is
// nothing for a later process to clean up or second-guess.
//
// A zero StateLock holds nothing. That is what a caller gets when the lock could not be
// taken for a reason other than another process holding it — see AcquireStateLock.
type StateLock struct {
	f *os.File
}

// stateLockHolder is what a holder records about itself in the lock file, so a process that
// is refused can say who has it. It is written after the lock is taken and read only by a
// process that failed to take it, which means a holder is alive and its record current.
type stateLockHolder struct {
	PID       int       `json:"pid"`
	Repo      string    `json:"repo"`
	StartedAt time.Time `json:"started_at"`
}

// StateLockHeldError reports that another cs process holds the state lock.
type StateLockHeldError struct {
	holder stateLockHolder
	known  bool
}

func (e *StateLockHeldError) Error() string {
	if desc := e.Describe(); desc != "" {
		return fmt.Sprintf("another claude-squad process holds the state lock: %s", desc)
	}
	return "another claude-squad process holds the state lock"
}

// Describe names the process holding the lock, for a message to the user. It is empty when
// the holder left no usable record — it had taken the lock but had not yet written one, or
// wrote something this version cannot read.
func (e *StateLockHeldError) Describe() string {
	if !e.known || e.holder.PID == 0 {
		return ""
	}
	desc := fmt.Sprintf("PID %d", e.holder.PID)
	if !e.holder.StartedAt.IsZero() {
		desc += fmt.Sprintf(", started %s", describeStart(e.holder.StartedAt, time.Now()))
	}
	if e.holder.Repo != "" {
		desc += fmt.Sprintf(" in %s", e.holder.Repo)
	}
	return desc
}

// describeStart renders when the holder started: the time alone for a process started
// today, which is the ordinary case, and the date as well for an older one.
func describeStart(started, now time.Time) string {
	started, now = started.Local(), now.Local()
	if started.Year() == now.Year() && started.YearDay() == now.YearDay() {
		return started.Format("15:04")
	}
	return started.Format("Jan 2 15:04")
}

// AcquireStateLock takes the state lock on behalf of a process working in repo, which is
// recorded so that a process refused the lock can name this one.
//
// It returns a *StateLockHeldError when another cs process holds the lock, and that is the
// only error it returns: any other failure — no config directory, a home directory on a
// filesystem that does not implement locking — is logged and reported as success, leaving
// the caller with a lock that holds nothing. The lock is a safety net over an existing
// arrangement, and refusing to start on a filesystem quirk would be a worse failure than
// the one it prevents.
func AcquireStateLock(repo string) (*StateLock, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		log.WarningLog.Printf("could not locate the state lock, continuing without it: %v", err)
		return &StateLock{}, nil
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		log.WarningLog.Printf("could not create the config directory for the state lock, continuing without it: %v", err)
		return &StateLock{}, nil
	}

	// O_CLOEXEC, which os.OpenFile sets, matters here: an advisory lock lives on the open
	// file description, so a child process inheriting this descriptor would keep the lock
	// alive after this process exits. The daemon is started as exactly such a child.
	f, err := os.OpenFile(filepath.Join(configDir, StateLockFileName), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		log.WarningLog.Printf("could not open the state lock, continuing without it: %v", err)
		return &StateLock{}, nil
	}

	locked, err := tryLockFile(f)
	if err != nil {
		log.WarningLog.Printf("could not take the state lock, continuing without it: %v", err)
		_ = f.Close()
		return &StateLock{}, nil
	}
	if !locked {
		held := &StateLockHeldError{}
		held.holder, held.known = readStateLockHolder(f)
		_ = f.Close()
		return nil, held
	}

	writeStateLockHolder(f, repo)
	return &StateLock{f: f}, nil
}

// Release gives up the lock. It is safe to call on a lock that holds nothing.
func (l *StateLock) Release() {
	if l == nil || l.f == nil {
		return
	}
	// Leave no record of a process that is gone. Nothing reads a record written by a dead
	// holder — only a process refused the lock reads one, and being refused means a live
	// holder wrote it — but a file naming a dead PID is a puzzle for whoever looks.
	_ = l.f.Truncate(0)
	unlockFile(l.f)
	_ = l.f.Close()
	l.f = nil
}

// writeStateLockHolder records who holds the lock. Failing to write it costs nothing but the
// detail in another process's error message, so it is logged and otherwise ignored.
func writeStateLockHolder(f *os.File, repo string) {
	data, err := json.Marshal(stateLockHolder{
		PID:       os.Getpid(),
		Repo:      repo,
		StartedAt: time.Now(),
	})
	if err != nil {
		log.WarningLog.Printf("failed to marshal the state lock holder: %v", err)
		return
	}
	if err := f.Truncate(0); err != nil {
		log.WarningLog.Printf("failed to clear the state lock file: %v", err)
		return
	}
	if _, err := f.WriteAt(data, 0); err != nil {
		log.WarningLog.Printf("failed to record the state lock holder: %v", err)
	}
}

// readStateLockHolder reads the record the holder left. ok is false when there is nothing
// usable there, which a caller reports as an unidentified holder rather than an error.
func readStateLockHolder(f *os.File) (holder stateLockHolder, ok bool) {
	// The record is written whole and small enough for one write, but a reader can still
	// arrive between the holder taking the lock and writing it.
	data := make([]byte, 4096)
	n, err := f.ReadAt(data, 0)
	if n == 0 {
		if err != nil {
			log.WarningLog.Printf("failed to read the state lock holder: %v", err)
		}
		return stateLockHolder{}, false
	}
	if err := json.Unmarshal(data[:n], &holder); err != nil {
		return stateLockHolder{}, false
	}
	return holder, true
}
