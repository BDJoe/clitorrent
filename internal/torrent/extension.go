package torrent

import (
	"errors"
	"fmt"
	"log"
	"math"
)

type Extension struct {
	SupportedExtensions map[string]int
	MetaDataSize        int
}

type Metadata struct {
	Metadata       []byte
	PiecesReceived int
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

func (c *Client) handleExtension(buf []byte) {
	if c.Extension.SupportedExtensions == nil {
		c.Extension.SupportedExtensions = make(map[string]int)
	}
	if buf[0] == 0 {
		msg, err := parseExtensionHandshake(buf[1:])
		if err != nil {
			log.Printf("%s\n", err)
			return
		}
		for ext, msgId := range msg.M {
			c.Extension.SupportedExtensions[ext] = msgId.(int)
		}
		c.Extension.MetaDataSize = msg.MetadataSize
		return
	}
	if buf[0] == 3 {
		msg, err := parseExtensionMessage(buf[1:])
		if err != nil {
			log.Printf("error parsing metadata message: %s\n", err)
			return
		}
		if msg.MsgType == 1 {
			copy(c.Metadata.Metadata[msg.Piece*MetadataPieceSize:], msg.MetadataChunk)
			c.PiecesReceived++
			fmt.Printf("Piece Size: %d\n", len(msg.MetadataChunk))
		} else {
			fmt.Printf("Metadata message: %+v", msg)
		}
	}
}

func (c *Client) getMetadata() ([]byte, error) {
	id, supported := c.Extension.SupportedExtensions["ut_metadata"]
	if !supported {
		return nil, errors.New("no metadata extension")
	}
	c.Metadata.Metadata = make([]byte, c.Extension.MetaDataSize)
	numPieces := int(math.Ceil(float64(c.Extension.MetaDataSize) / float64(MetadataPieceSize)))

	for i := 0; i < numPieces; i++ {
		requestMsg := MetadataExtensionMessage{ExtensionMessageID: ExtensionMessageID{MsgId: id}, MsgType: 0, Piece: i}
		encoded := serializeExtensionMessage(requestMsg)
		err := sendMessage(c, encoded)
		if err != nil {
			return nil, err
		}
		for c.PiecesReceived <= 1 {
			msg, err := c.Read()
			if err != nil {
				return nil, err
			}
			err = c.handleMessage(msg)
			if err != nil {
				return nil, err
			}
		}
	}
	return c.Metadata.Metadata, nil
}
