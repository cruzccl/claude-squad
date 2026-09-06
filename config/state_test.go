package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPromoteKnownRepo_MostRecentFirstWithoutDuplicates(t *testing.T) {
	list := PromoteKnownRepo([]string{"/a", "/b"}, "/c", 10)
	require.Equal(t, []string{"/c", "/a", "/b"}, list)

	list = PromoteKnownRepo(list, "/b", 10)
	require.Equal(t, []string{"/b", "/c", "/a"}, list,
		"choosing a repository again moves it to the front rather than repeating it")
}

func TestPromoteKnownRepo_ForgetsTheOldestPastTheLimit(t *testing.T) {
	list := PromoteKnownRepo([]string{"/a", "/b", "/c"}, "/d", 3)
	require.Equal(t, []string{"/d", "/a", "/b"}, list)
}

func TestPromoteKnownRepo_LeavesTheStoredListAlone(t *testing.T) {
	stored := []string{"/a", "/b"}
	PromoteKnownRepo(stored, "/c", 10)
	require.Equal(t, []string{"/a", "/b"}, stored, "the caller's slice is not written through")
}
