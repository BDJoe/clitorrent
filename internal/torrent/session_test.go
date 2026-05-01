package torrent

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataCache(t *testing.T) {
	prog := tea.Program{}
	session, err := OpenTorrent("/home/joe/Downloads/50Matt_Dinniman___Carl_s_Doomsday_Scenario_Dungeon_Crawler_Carl__Book_.torrent", "/home/joe/Downloads/", &prog, 0)
	require.NoError(t, err)
	assert.Equal(t, len(session.PieceHashes), len(session.PiecesDone))
}

func TestDownload(t *testing.T) {
	prog := tea.Program{}
	session, err := OpenTorrent("/home/joe/Downloads/50Matt_Dinniman___Carl_s_Doomsday_Scenario_Dungeon_Crawler_Carl__Book_.torrent", "/home/joe/Downloads/", &prog, 0)
	require.NoError(t, err)
	err = session.Download(&prog, 0)
	require.NoError(t, err)
}
