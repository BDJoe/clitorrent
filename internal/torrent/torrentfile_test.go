package torrent

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetCachedTorrents(t *testing.T) {
	sessions, err := GetCachedTorrents()
	fmt.Printf("%+v\n", sessions[0])
	require.NoError(t, err)
	require.Len(t, sessions, 1)
}
