package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useTempHome points the config directory at a directory of this test's own, so a test
// never touches the state lock of a cs the developer is running.
func useTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // what os.UserHomeDir reads on Windows
	return home
}

func TestAcquireStateLockRefusesASecondHolder(t *testing.T) {
	home := useTempHome(t)

	first, err := AcquireStateLock("/repos/one")
	require.NoError(t, err)
	defer first.Release()

	// An advisory lock belongs to the open file description, not to the process, so a
	// second acquisition here is refused exactly as another cs would be.
	second, err := AcquireStateLock("/repos/two")
	require.Error(t, err)
	assert.Nil(t, second)

	var held *StateLockHeldError
	require.True(t, errors.As(err, &held), "expected a StateLockHeldError, got %v", err)
	desc := held.Describe()
	assert.Contains(t, desc, "PID "+strconv.Itoa(os.Getpid()))
	assert.Contains(t, desc, "/repos/one")

	assert.FileExists(t, filepath.Join(home, ".claude-squad", StateLockFileName))
}

func TestStateLockIsAvailableAgainAfterRelease(t *testing.T) {
	useTempHome(t)

	first, err := AcquireStateLock("/repos/one")
	require.NoError(t, err)
	first.Release()

	second, err := AcquireStateLock("/repos/two")
	require.NoError(t, err)
	defer second.Release()

	// Releasing twice, and releasing a lock that holds nothing, are both harmless: the
	// caller defers Release without knowing which it has.
	first.Release()
	(&StateLock{}).Release()
}

func TestStateLockHolderIsUnnamedWhenTheRecordIsUnreadable(t *testing.T) {
	home := useTempHome(t)

	lock, err := AcquireStateLock("/repos/one")
	require.NoError(t, err)
	defer lock.Release()

	// A holder that has taken the lock but not yet written its record, seen from a process
	// that arrives in between.
	require.NoError(t, os.WriteFile(filepath.Join(home, ".claude-squad", StateLockFileName), nil, 0644))

	_, err = AcquireStateLock("/repos/two")
	var held *StateLockHeldError
	require.True(t, errors.As(err, &held), "expected a StateLockHeldError, got %v", err)
	assert.Empty(t, held.Describe())
	assert.Equal(t, "another claude-squad process holds the state lock", held.Error())
}

func TestDescribeStart(t *testing.T) {
	now := time.Date(2026, time.September, 6, 18, 30, 0, 0, time.Local)

	assert.Equal(t, "10:30", describeStart(now.Add(-8*time.Hour), now))
	assert.Equal(t, "Sep 5 23:00", describeStart(now.Add(-20*time.Hour+30*time.Minute), now))
}
