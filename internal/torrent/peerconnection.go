package torrent

import (
	"bytes"
	"fmt"
	"net"
	"time"
)

// A PeerConnection is a TCP connection with a peer
type PeerConnection struct {
	Conn           net.Conn
	AmChoked       bool
	PeerChoked     bool
	AmInterested   bool
	PeerInterested bool
	PeerBitfield   Bitfield
	Extension
	Metadata
	InfoHash   [20]byte
	PeerID     [20]byte
	Address    Peer
	PieceState *pieceProgress
}

type PeerMessage struct {
	peer    *PeerConnection
	message *Message
}

func (c *PeerConnection) completeHandshake(peerID [20]byte) error {

	req := newHandshake(c.InfoHash, peerID)
	_, err := c.Conn.Write(req.SerializeMagnetHandshake())
	if err != nil {
		return fmt.Errorf("Failed to write handshake: %v", err)
	}
	res, err := readHandshake(c.Conn)
	if err != nil {
		return fmt.Errorf("Failed to read handshake: %v", err)
	}
	if !bytes.Equal(res.InfoHash[:], c.InfoHash[:]) {
		return fmt.Errorf("Expected infohash %x but got %x", res.InfoHash, c.InfoHash)
	}
	if res.HasExtensions {
		m := serializeExtensionHandshake()
		_, err := c.Conn.Write(m)
		if err != nil {
			return err
		}
	}
	c.PeerID = res.PeerID
	return nil
}

//func (c *PeerConnection) completeMagnetHandshake(peerID [20]byte) error {
//	req := newHandshake(c.InfoHash, peerID)
//	_, err := c.Conn.Write(req.SerializeMagnetHandshake())
//	if err != nil {
//		return err
//	}
//
//	res, err := readHandshake(c.Conn)
//	if err != nil {
//		return err
//	}
//	if !bytes.Equal(res.InfoHash[:], c.InfoHash[:]) {
//		return fmt.Errorf("Expected infohash %x but got %x", res.InfoHash, c.InfoHash)
//	}
//
//	if res.HasExtensions {
//		m := serializeExtensionHandshake()
//		_, err := c.Conn.Write(m)
//		if err != nil {
//			return err
//		}
//	}
//	c.PeerID = res.PeerID
//
//	return nil
//}

func (c *PeerConnection) recvBitfield() error {

	msg, err := readMessage(c)
	if err != nil {
		return err
	}
	if msg == nil {
		err := fmt.Errorf("Expected bitfield but got %s", msg)
		return err
	}
	if msg.ID != MsgBitfield {
		err := fmt.Errorf("Expected bitfield but got ID %d", msg.ID)
		return err
	}
	c.PeerBitfield = msg.Payload

	return nil
}

// New connects with a peer, completes a handshake, and receives a handshake
// returns an err if any of those fail
func newClient(peer Peer, infoHash [20]byte, peerID [20]byte, bitfield *Bitfield) (*PeerConnection, error) {
	conn, err := net.DialTimeout("tcp", peer.String(), 1*time.Second)
	if err != nil {
		return nil, fmt.Errorf("Failed to connect to peer: %v", err)
	}

	client := PeerConnection{
		Conn:           conn,
		AmChoked:       true,
		PeerChoked:     true,
		AmInterested:   false,
		PeerInterested: false,
		InfoHash:       infoHash,
		Address:        peer,
	}

	err = client.completeHandshake(peerID)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("Failed to complete handshake: %v", err)
	}

	//err = client.recvBitfield()
	//if err != nil {
	//	conn.Close()
	//	return nil, err
	//}
	if HasPieces(*bitfield) {
		err = client.SendBitfield(bitfield)
		if err != nil {
			return nil, fmt.Errorf("Failed to send bitfield: %v", err)
		}
	}

	return &client, nil
}

// New connects with a peer, completes a handshake, and receives a handshake
// returns an err if any of those fail
//func newMagnetClient(peer Peer, peerID, infoHash [20]byte) (*PeerConnection, error) {
//	conn, err := net.DialTimeout("tcp", peer.String(), 3*time.Second)
//	if err != nil {
//		return nil, err
//	}
//	client := PeerConnection{
//		Conn:           conn,
//		AmChoked:       true,
//		PeerChoked:     true,
//		AmInterested:   false,
//		PeerInterested: false,
//		InfoHash:       infoHash,
//		Address:        peer,
//	}
//	err = client.completeMagnetHandshake(peerID)
//	if err != nil {
//		conn.Close()
//		return nil, err
//	}
//
//	//err = client.recvBitfield()
//	//if err != nil {
//	//	conn.Close()
//	//	return nil, err
//	//}
//
//	return &client, nil
//}

// Read reads and consumes a message from the connection
func (c *PeerConnection) Read() (*Message, error) {
	msg, err := readMessage(c)
	if err != nil {
		return nil, err
	}
	return msg, err
}

//// ReadMessages reads and consumes a message from the connection
//func (c *PeerConnection) ReadMessages() error {
//	for {
//		msg, err := readMessage(c)
//		if err != nil {
//			if errors.Is(err, os.ErrDeadlineExceeded) {
//				return nil
//			}
//			fmt.Println("error reading messages:", err)
//			return err
//		}
//		err = c.handleMessage(msg)
//		if err != nil {
//			return err
//		}
//	}
//	return nil
//}

//func (c *PeerConnection) handleMessage(msg *Message) error {
//	if msg == nil { // keep-alive
//		return nil
//	}
//
//	logFile, err := os.OpenFile("app.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
//	if err != nil {
//		log.Fatalf("Failed to open log file: %v", err)
//	}
//	defer logFile.Close()
//
//	log.SetOutput(logFile)
//	log.SetFlags(log.LstdFlags | log.Lshortfile)
//
//	log.Println("Message Type: ", msg.ID)
//
//	switch msg.ID {
//	case MsgChoke:
//		c.AmChoked = true
//	case MsgUnchoke:
//		c.AmChoked = false
//	case MsgInterested:
//		fmt.Println(msg.String())
//		c.PeerInterested = true
//		c.SendUnchoke()
//	case MsgNotInterested:
//		c.PeerInterested = false
//	case MsgHave:
//		index, err := parseHave(msg)
//		if err != nil {
//			return err
//		}
//		c.PeerBitfield.SetPiece(index)
//	case MsgBitfield:
//		c.PeerBitfield = msg.Payload
//		c.SendInterested()
//	case MsgRequest:
//		fmt.Println(msg.String())
//		err := c.HandleRequest(msg)
//		if err != nil {
//			return err
//		}
//	case MsgExtended:
//		c.processExtension(msg.Payload)
//	case MsgPiece:
//		n, err := parsePiece(c.PieceState.index, c.PieceState.buf, msg)
//		if err != nil {
//			return err
//		}
//		c.PieceState.downloaded += n
//		c.PieceState.backlog--
//
//	default:
//		return fmt.Errorf("unrecognized message ID %d", msg.ID)
//	}
//	return nil
//}

func (c *PeerConnection) HandleExtension(msg *Message) error {
	if msg == nil { // keep-alive
		return nil
	}

	if msg.ID == MsgExtended {
		c.processExtension(msg.Payload)
	}
	return nil
}

func (c *PeerConnection) peerListener(msgChan chan PeerMessage) {
	for {
		if c == nil || c.Conn == nil {
			return
		}
		msg, err := c.Read()
		if err != nil {
			break
		}
		msgChan <- PeerMessage{c, msg}
	}
	msgChan <- PeerMessage{c, nil}
}

func (c *PeerConnection) SendPiece(msg *Message) error {
	err := sendMessage(c, msg.Serialize())
	return err
}

// SendRequest sends a Request message to the peer
func (c *PeerConnection) SendRequest(index, begin, length int) error {
	req := formatRequest(index, begin, length)
	err := sendMessage(c, req.Serialize())

	return err
}

// SendCancel sends a Cancel message to the peer
func (c *PeerConnection) SendCancel(index, begin, length int) error {
	req := formatCancel(index, begin, length)
	err := sendMessage(c, req.Serialize())

	return err
}

// SendBitfield sends a Request message to the peer
func (c *PeerConnection) SendBitfield(bitfield *Bitfield) error {
	req := formatBitfield(*bitfield)
	err := sendMessage(c, req.Serialize())

	return err
}

// SendInterested sends an AmInterested message to the peer
func (c *PeerConnection) SendInterested() error {
	msg := Message{ID: MsgInterested}
	err := sendMessage(c, msg.Serialize())

	return err
}

// SendNotInterested sends a NotInterested message to the peer
func (c *PeerConnection) SendNotInterested() error {
	msg := Message{ID: MsgNotInterested}
	err := sendMessage(c, msg.Serialize())

	return err
}

// SendUnchoke sends an Unchoke message to the peer
func (c *PeerConnection) SendUnchoke() error {
	msg := Message{ID: MsgUnchoke}
	err := sendMessage(c, msg.Serialize())

	return err
}

// SendChoke sends an Unchoke message to the peer
func (c *PeerConnection) SendChoke() error {
	msg := Message{ID: MsgChoke}
	err := sendMessage(c, msg.Serialize())

	return err
}

// SendHave sends a Have message to the peer
func (c *PeerConnection) SendHave(index int) error {
	msg := formatHave(index)
	err := sendMessage(c, msg.Serialize())

	return err
}
