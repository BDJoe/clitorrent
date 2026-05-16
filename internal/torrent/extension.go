package torrent

import (
	"errors"
	"fmt"
	"math"
)

type Extension struct {
	SupportedExtensions map[string]int
	MetaDataSize        int
}

type Metadata struct {
	Metadata        []byte
	PiecesReceived  int
	PiecesRequested int
}

type MetadataExtensionMessage struct {
	ExtensionMessageID
	MsgType       int
	Piece         int
	MetadataChunk []byte
	TotalSize     int
}

type ExtensionMessageID struct {
	MsgId int
}

const MetadataPieceSize = 16384

func (c *PeerConnection) processExtension(buf []byte) {
	if c.Extension.SupportedExtensions == nil {
		c.Extension.SupportedExtensions = make(map[string]int)
	}
	if buf[0] == 0 {
		msg, err := parseExtensionHandshake(buf[1:])
		if err != nil {
			//log.Printf("error parsing handshake: %s\n", err)
			return
		}
		for ext, msgId := range msg.M {
			c.Extension.SupportedExtensions[ext] = msgId.(int)
		}
		c.Extension.MetaDataSize = msg.MetadataSize
		return
	}

	msg, err := parseExtensionMessage(buf[1:])
	if err != nil {
		fmt.Printf("error parsing metadata message: %s\n", err)
		return
	}
	if msg.MsgType == 1 {
		copy(c.Metadata.Metadata[msg.Piece*MetadataPieceSize:], msg.MetadataChunk)
		c.PiecesReceived++
	} else {
		return
	}
}

func (c *PeerConnection) getMetadata() ([]byte, error) {
	id, supported := c.Extension.SupportedExtensions["ut_metadata"]
	if !supported {
		return nil, errors.New("no metadata extension")
	}
	c.Metadata.Metadata = make([]byte, c.Extension.MetaDataSize)
	numPieces := int(math.Ceil(float64(c.Extension.MetaDataSize) / float64(MetadataPieceSize)))

	for c.PiecesReceived < numPieces {
		if c.PiecesRequested < numPieces {
			for i := 0; i < numPieces; i++ {
				requestMsg := MetadataExtensionMessage{ExtensionMessageID: ExtensionMessageID{MsgId: id}, MsgType: 0, Piece: i}
				encoded := serializeExtensionMessage(requestMsg)

				err := sendMessage(c, encoded)
				if err != nil {
					//fmt.Printf("error sending metadata message: %s\n", err)
					return nil, err
				}
				c.PiecesReceived++
			}
		}
	}
	return c.Metadata.Metadata, nil
}
