package torrent

import (
	"fmt"
	"io"
)

// A Handshake is a special message that a peer uses to identify itself
type Handshake struct {
	Pstr          string
	InfoHash      [20]byte
	PeerID        [20]byte
	HasExtensions bool
}

// New creates a new handshake with the standard pstr
func newHandshake(infoHash, peerID [20]byte) *Handshake {
	return &Handshake{
		Pstr:     "BitTorrent protocol",
		InfoHash: infoHash,
		PeerID:   peerID,
	}
}

// SerializeMagnetHandshake serializes the handshake for a magnet link to a buffer
func (h *Handshake) SerializeMagnetHandshake() []byte {
	buf := make([]byte, 68)
	buf[0] = byte(len(h.Pstr))
	copy(buf[1:], h.Pstr)
	buf[25] = 16
	copy(buf[28:], h.InfoHash[:])
	copy(buf[48:], h.PeerID[:])
	return buf
}

// Serialize serializes the handshake to a buffer
func (h *Handshake) Serialize() []byte {
	buf := make([]byte, len(h.Pstr)+49)
	buf[0] = byte(len(h.Pstr))
	curr := 1
	curr += copy(buf[curr:], h.Pstr)
	curr += copy(buf[curr:], make([]byte, 8)) // 8 reserved bytes
	curr += copy(buf[curr:], h.InfoHash[:])
	curr += copy(buf[curr:], h.PeerID[:])
	return buf
}

// Read parses a handshake from a stream
func readHandshake(r io.Reader) (*Handshake, error) {
	handshakeBuf := make([]byte, 68)
	_, err := io.ReadFull(r, handshakeBuf)
	if err != nil {
		return nil, err
	}
	if len(handshakeBuf) != 68 {
		err := fmt.Errorf("handshake response length invalid, got :%d", len(handshakeBuf))
		return nil, err
	}

	var infoHash, peerID [20]byte

	copy(infoHash[:], handshakeBuf[28:48])
	copy(peerID[:], handshakeBuf[48:])

	h := Handshake{
		Pstr:     string(handshakeBuf[1:20]),
		InfoHash: infoHash,
		PeerID:   peerID,
	}
	if handshakeBuf[25]&16 == 16 {
		h.HasExtensions = true
	}
	return &h, nil
}
