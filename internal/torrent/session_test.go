package torrent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataCache(t *testing.T) {
	session, err := OpenTorrent("/home/joe/Downloads/50Matt_Dinniman___Carl_s_Doomsday_Scenario_Dungeon_Crawler_Carl__Book_.torrent", "/home/joe/Downloads/")
	require.NoError(t, err)
	assert.Equal(t, len(session.PieceHashes), len(session.PiecesDone))
}
