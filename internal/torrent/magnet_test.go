package torrent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMagnetParse(t *testing.T) {
	_, err := GetMetadata()
	require.NoError(t, err)
}
