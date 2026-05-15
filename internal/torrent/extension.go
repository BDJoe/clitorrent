package torrent

import (
	"errors"
	"math"
)

type Extension struct {
	SupportedExtensions map[string]int
	MetaDataSize        int
}

type Metadata struct {
	Metadata       []byte
	PiecesReceived int
	NumPieces      int
	Pieces         []bool
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

func (c *PeerConnection) handleExtension(buf []byte) {
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
		id, supported := c.Extension.SupportedExtensions["ut_metadata"]
		if !supported {
			return
		}
		c.Extension.MetaDataSize = msg.MetadataSize
		c.NumPieces = int(math.Ceil(float64(c.Extension.MetaDataSize) / float64(MetadataPieceSize)))
		c.Pieces = make([]bool, c.NumPieces)
		requestMsg := MetadataExtensionMessage{ExtensionMessageID: ExtensionMessageID{MsgId: id}, MsgType: 0, Piece: 0}
		encoded := serializeExtensionMessage(requestMsg)

		_ = sendMessage(c, encoded)
		return
	}
	if buf[0] == 3 {
		msg, err := parseExtensionMessage(buf[1:])
		if err != nil {
			//log.Printf("error parsing metadata message: %s\n", err)
			return
		}
		if msg.MsgType == 1 {
			copy(c.Metadata.Metadata[msg.Piece*MetadataPieceSize:], msg.MetadataChunk)
			c.PiecesReceived++
			c.Pieces[msg.Piece] = true
			id, supported := c.Extension.SupportedExtensions["ut_metadata"]
			if !supported {
				return
			}
			for i, have := range c.Pieces {
				if !have {
					requestMsg := MetadataExtensionMessage{ExtensionMessageID: ExtensionMessageID{MsgId: id}, MsgType: 0, Piece: i}
					encoded := serializeExtensionMessage(requestMsg)

					_ = sendMessage(c, encoded)
					return
				}
			}
		} else {
			return
		}
	}
}

func (c *PeerConnection) getMetadata() ([]byte, error) {
	id, supported := c.Extension.SupportedExtensions["ut_metadata"]
	if !supported {
		return nil, errors.New("no metadata extension")
	}
	c.Metadata.Metadata = make([]byte, c.Extension.MetaDataSize)
	c.NumPieces = int(math.Ceil(float64(c.Extension.MetaDataSize) / float64(MetadataPieceSize)))

	for i := 1; i < c.NumPieces; i++ {
		requestMsg := MetadataExtensionMessage{ExtensionMessageID: ExtensionMessageID{MsgId: id}, MsgType: 0, Piece: i}
		encoded := serializeExtensionMessage(requestMsg)

		err := sendMessage(c, encoded)
		if err != nil {
			//fmt.Printf("error sending metadata message: %s\n", err)
			return nil, err
		}
		for c.PiecesReceived <= i {
			msg, err := c.Read()
			if err != nil {
				//fmt.Printf("error reading message from extension: %s\n", err)
				return nil, err
			}
			err = c.handleMessage(msg)
			if err != nil {
				//fmt.Printf("error handling message: %s\n", err)
				return nil, err
			}
		}
	}
	return c.Metadata.Metadata, nil
}
