package ui

import (
	"claude-squad/session"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/stretchr/testify/require"
)

func newTestList(titles ...string) *List {
	s := spinner.New()
	l := NewList(&s, false)
	for _, t := range titles {
		inst, _ := session.NewInstance(session.InstanceOptions{
			Title:   t,
			Path:    ".",
			Program: "echo",
		})
		l.AddInstance(inst)
	}
	return l
}

func TestMoveUp(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(1) // select "b"

	moved := l.MoveUp()
	require.True(t, moved)
	require.Equal(t, 0, l.selectedIdx)
	require.Equal(t, "b", l.items[0].Title)
	require.Equal(t, "a", l.items[1].Title)
	require.Equal(t, "c", l.items[2].Title)
}

func TestMoveUp_AtTop(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(0)

	moved := l.MoveUp()
	require.False(t, moved)
	require.Equal(t, 0, l.selectedIdx)
	require.Equal(t, "a", l.items[0].Title)
}

func TestMoveDown(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(1) // select "b"

	moved := l.MoveDown()
	require.True(t, moved)
	require.Equal(t, 2, l.selectedIdx)
	require.Equal(t, "a", l.items[0].Title)
	require.Equal(t, "c", l.items[1].Title)
	require.Equal(t, "b", l.items[2].Title)
}

func TestMoveDown_AtBottom(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(2)

	moved := l.MoveDown()
	require.False(t, moved)
	require.Equal(t, 2, l.selectedIdx)
	require.Equal(t, "c", l.items[2].Title)
}

func TestMoveWithSingleItem(t *testing.T) {
	l := newTestList("only")
	l.SetSelectedInstance(0)

	require.False(t, l.MoveUp())
	require.False(t, l.MoveDown())
}

// newListInRepos builds a list holding one unstarted session per given repository path,
// which is the state the list is in while a newly created session is starting.
func newListInRepos(paths ...string) *List {
	s := spinner.New()
	l := NewList(&s, false)
	l.SetSize(120, 40)
	for i, path := range paths {
		inst, _ := session.NewInstance(session.InstanceOptions{
			Title:   fmt.Sprintf("session-%d", i),
			Path:    path,
			Program: "echo",
		})
		inst.Branch = fmt.Sprintf("branch-%d", i)
		l.AddInstance(inst)
	}
	return l
}

func TestRepoNameShownWhenReposDiffer(t *testing.T) {
	root := t.TempDir()
	web := filepath.Join(root, "acme-web")
	tools := filepath.Join(root, "acme-tools")
	require.NoError(t, os.MkdirAll(web, 0o755))
	require.NoError(t, os.MkdirAll(tools, 0o755))

	rendered := newListInRepos(web, tools).String()
	require.Contains(t, rendered, "(acme-web)")
	require.Contains(t, rendered, "(acme-tools)")
}

func TestRepoNameShownWithSingleRepo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "acme-web")
	require.NoError(t, os.MkdirAll(repo, 0o755))

	rendered := newListInRepos(repo, repo).String()
	require.Equal(t, 2, strings.Count(rendered, "(acme-web)"))
}
