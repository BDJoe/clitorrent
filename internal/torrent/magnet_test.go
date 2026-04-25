package torrent

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMagnetParse(t *testing.T) {
	info, err := GetMetadata()
	require.NoError(t, err)
	fmt.Println(info.Name)
}
