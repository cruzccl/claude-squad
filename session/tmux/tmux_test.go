package tmux

import (
	cmd2 "claude-squad/cmd"
	"claude-squad/log"
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"claude-squad/cmd/cmd_test"

	"github.com/stretchr/testify/require"
)

type MockPtyFactory struct {
	t *testing.T

	// Array of commands and the corresponding file handles representing PTYs.
	cmds  []*exec.Cmd
	files []*os.File
}

func (pt *MockPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	filePath := filepath.Join(pt.t.TempDir(), fmt.Sprintf("pty-%s-%d", pt.t.Name(), rand.Int31()))
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err == nil {
		pt.cmds = append(pt.cmds, cmd)
		pt.files = append(pt.files, f)
	}
	return f, err
}

func (pt *MockPtyFactory) Close() {}

func NewMockPtyFactory(t *testing.T) *MockPtyFactory {
	return &MockPtyFactory{
		t: t,
	}
}

func TestSanitizeName(t *testing.T) {
	session := NewTmuxSession("asdf", "program")
	require.Equal(t, TmuxPrefix+"asdf", session.sanitizedName)

	session = NewTmuxSession("a sd f . . asdf", "program")
	require.Equal(t, TmuxPrefix+"asdf__asdf", session.sanitizedName)
}

func TestStartTmuxSession(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)

	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") && !created {
				created = true
				return fmt.Errorf("session already exists")
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("output"), nil
		},
	}

	workdir := t.TempDir()
	session := newTmuxSession("test-session", "claude", ptyFactory, cmdExec)

	err := session.Start(workdir)
	require.NoError(t, err)
	require.Equal(t, 2, len(ptyFactory.cmds))
	require.Equal(t, fmt.Sprintf("tmux new-session -d -s claudesquad_test-session -c %s claude", workdir),
		cmd2.ToString(ptyFactory.cmds[0]))
	require.Equal(t, "tmux attach-session -t claudesquad_test-session",
		cmd2.ToString(ptyFactory.cmds[1]))

	require.Equal(t, 2, len(ptyFactory.files))

	// File should be closed.
	_, err = ptyFactory.files[0].Stat()
	require.Error(t, err)
	// File should be open
	_, err = ptyFactory.files[1].Stat()
	require.NoError(t, err)
}

// A tmux server that has gone away (reboot, crash, `tmux kill-server`) takes every session
// with it. attach-session against a missing session still forks successfully, so Restore
// has to check for the session itself or it reports success while attached to nothing.
func TestRestoreReturnsErrSessionNotFoundWhenSessionIsGone(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") {
				return fmt.Errorf("can't find session")
			}
			return nil
		},
	}

	session := NewTmuxSessionWithDeps("gone", "program", ptyFactory, cmdExec)
	err := session.Restore()

	require.ErrorIs(t, err, ErrSessionNotFound)
	require.Empty(t, ptyFactory.cmds, "should not have opened a PTY for a session that does not exist")
}

func TestRestoreAttachesWhenSessionExists(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error { return nil },
	}

	session := NewTmuxSessionWithDeps("alive", "program", ptyFactory, cmdExec)
	require.NoError(t, session.Restore())
	require.Len(t, ptyFactory.cmds, 1)
	require.Contains(t, ptyFactory.cmds[0].String(), "attach-session")
}

// While a session is attached, claude-squad reads stdin before tmux does, so the meaning
// of a keystroke is decided here: it either ends the attachment for a reason the caller
// acts on, or it belongs to the session and is forwarded untouched.

func TestAttachInputTellsGoingBackFromSwitchingSides(t *testing.T) {
	// Ctrl+Q hands the terminal back to the session list.
	back := readAttachAction([]byte{detachKey})
	require.Equal(t, attachDetach, back)
	reason, detaches := back.detachReason()
	require.True(t, detaches)
	require.Equal(t, DetachedToLayout, reason, "ctrl+q means the layout, not the other side")

	// Ctrl+Y hands it to the session's other side instead.
	switched := readAttachAction([]byte{switchKey})
	require.Equal(t, attachSwitch, switched)
	reason, detaches = switched.detachReason()
	require.True(t, detaches)
	require.Equal(t, DetachedToSwitch, reason, "ctrl+y detaches only to attach the other side")
}

func TestAttachInputForwardsEverythingElse(t *testing.T) {
	for name, input := range map[string][]byte{
		"an ordinary keystroke":            {'a'},
		"a key claude-squad does not want": {3}, // ctrl+c is the program's business
		"a paste that contains ctrl+q":     {detachKey, 'a'},
		"a paste that contains ctrl+y":     {switchKey, switchKey},
		"nothing at all":                   {},
	} {
		action := readAttachAction(input)
		require.Equal(t, attachForward, action, name)
		_, detaches := action.detachReason()
		require.False(t, detaches, name)
	}
}

func TestDetachReasonBeforeAnyAttachIsTheLayout(t *testing.T) {
	session := NewTmuxSession("never-attached", "claude")
	require.Equal(t, DetachedToLayout, session.DetachReason(),
		"a session nobody switched away from must not claim the other side is wanted")
}

// An attachment ends for one of three reasons, and two of them can arrive at the same
// moment: the user pressing a key claude-squad keeps, and the tmux session going away
// under it. Whoever gets there first decides what the caller is told.

func TestEndAttachHappensOnceAndKeepsTheFirstReason(t *testing.T) {
	session := NewTmuxSession("ends-once", "claude")
	ch, _ := session.beginAttach()

	require.True(t, session.endAttach(DetachedSessionEnded), "the first end is the one that counts")
	<-ch // closed, so a caller waiting on the attachment is released
	require.Equal(t, DetachedSessionEnded, session.DetachReason())

	require.False(t, session.endAttach(DetachedToLayout), "a second end has nothing left to end")
	require.Equal(t, DetachedSessionEnded, session.DetachReason(),
		"the reason the caller is about to read must not be rewritten under it")
}

func TestAttachmentGenerationTellsAStaleReaderToStop(t *testing.T) {
	session := NewTmuxSession("generations", "claude")

	_, first := session.beginAttach()
	require.True(t, session.attachedAs(first))

	session.endAttach(DetachedSessionEnded)
	require.False(t, session.attachedAs(first), "an attachment that ended is not still running")

	// Switching sides attaches again straight away. The reader left over from the
	// attachment before must not start forwarding into the new one.
	_, second := session.beginAttach()
	require.False(t, session.attachedAs(first), "the old reader stays out of the new attachment")
	require.True(t, session.attachedAs(second))
}

// Leaving the shell with `exit`, or the agent's program quitting, kills the tmux session
// while claude-squad is attached to it. Detaching then finds nothing to go back to.
func TestDetachingFromASessionThatIsGoneEndsTheAttachmentInsteadOfPanicking(t *testing.T) {
	log.Initialize(false)
	defer log.Close()

	ptyFactory := NewMockPtyFactory(t)
	// The session does not exist until it is started, exists from then on, and is gone
	// again once the shell inside it has been left.
	unborn, gone := true, false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") {
				if gone {
					return fmt.Errorf("session does not exist")
				}
				if unborn {
					unborn = false
					return fmt.Errorf("session does not exist")
				}
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) { return []byte("output"), nil },
	}

	session := newTmuxSession("vanishing", "claude", ptyFactory, cmdExec)
	require.NoError(t, session.Start(t.TempDir()))
	ch, _ := session.beginAttach()
	session.ctx, session.cancel = context.WithCancel(context.Background())
	session.wg = &sync.WaitGroup{}

	gone = true
	require.NotPanics(t, func() { session.Detach() },
		"the way out of an attachment cannot be the thing that kills the program")

	<-ch
	require.Equal(t, DetachedSessionEnded, session.DetachReason(),
		"the caller is told the session ended rather than that the user asked for the list")
}
