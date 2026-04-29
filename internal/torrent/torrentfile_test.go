package torrent

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenMagnet(t *testing.T) {
	session, err := OpenMagnet(MagnetLink, "/home/joe/Downloads/")
	fmt.Printf("%+v\n", session)
	require.NoError(t, err)
	require.NotNil(t, session)
}
