package torrent

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestOpenMagnet(t *testing.T) {
	prog := tea.Program{}
	session, err := OpenMagnet(MagnetLink, "/home/joe/Downloads/", &prog, 0)
	require.NoError(t, err)
	require.NotNil(t, session)
}

func TestGetCachedTorrents(t *testing.T) {
	prog := tea.Program{}
	sessions, err := GetCachedTorrents(&prog)
	fmt.Printf("%+v\n", sessions[0])
	require.NoError(t, err)
	require.Len(t, sessions, 1)
}
