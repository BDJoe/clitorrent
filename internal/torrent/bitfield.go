package torrent

// A bitfield represents the pieces that a peer has
type Bitfield []byte

func newBitfield(length int) Bitfield {
	numBytes := (length + 7) / 8
	return make(Bitfield, numBytes)
}

// HasPiece tells if a bitfield has a particular index set
func (bf Bitfield) HasPiece(index int) bool {
	byteIndex := index / 8
	bitOffset := 7 - (index % 8)

	// silently discard invalid bounded index
	if byteIndex < 0 || byteIndex >= len(bf) {
		return false
	}
	return bf[byteIndex]&(1<<bitOffset) != 0
}

// SetPiece sets a bit in the bitfield
func (bf Bitfield) SetPiece(index int) {
	byteIndex := index / 8
	bitOffset := 7 - (index % 8)

	// silently discard invalid bounded index
	if byteIndex < 0 || byteIndex >= len(bf) {
		return
	}
	bf[byteIndex] |= 1 << bitOffset
}

// HasPieces checks if the bitfield has any downloaded pieces already
func HasPieces(bf Bitfield) bool {
	for _, b := range bf {
		if b != 0 {
			return true
		}
	}
	return false
}
