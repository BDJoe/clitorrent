package torrent

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
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

func (c *PeerConnection) completeHandshake(peerID [20]byte) (*Handshake, error) {
	c.Conn.SetDeadline(time.Now().Add(3 * time.Second))
	defer c.Conn.SetDeadline(time.Time{}) // Disable the deadline

	req := newHandshake(c.InfoHash, peerID)
	_, err := c.Conn.Write(req.Serialize())
	if err != nil {
		return nil, err
	}

	res, err := readHandshake(c.Conn)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(res.InfoHash[:], c.InfoHash[:]) {
		return nil, fmt.Errorf("Expected infohash %x but got %x", res.InfoHash, c.InfoHash)
	}

	return res, nil
}

func completeMagnetHandshake(conn net.Conn, infohash, peerID [20]byte) (*Handshake, error) {
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	defer conn.SetDeadline((time.Time{})) // Disable the deadline

	req := newHandshake(infohash, peerID)
	_, err := conn.Write(req.SerializeMagnetHandshake())
	if err != nil {
		return nil, err
	}

	res, err := readHandshake(conn)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(res.InfoHash[:], infohash[:]) {
		return nil, fmt.Errorf("Expected infohash %x but got %x", res.InfoHash, infohash)
	}

	if res.HasExtensions {
		m := serializeExtensionHandshake()
		_, err := conn.Write(m)
		if err != nil {
			return nil, err
		}
	}

	return res, nil
}

func (c *PeerConnection) recvBitfield() error {
	c.Conn.SetDeadline(time.Now().Add(5 * time.Second))
	defer c.Conn.SetDeadline(time.Time{}) // Disable the deadline

	msg, err := readMessage(c, 5*time.Second)
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
func newClient(peer Peer, peerID, infoHash [20]byte, bitfield *Bitfield) (*PeerConnection, error) {
	conn, err := net.DialTimeout("tcp", peer.String(), 3*time.Second)
	if err != nil {
		return nil, err
	}
	client := PeerConnection{
		Conn:           conn,
		AmChoked:       true,
		PeerChoked:     true,
		AmInterested:   false,
		PeerInterested: false,
		InfoHash:       infoHash,
	}
	_, err = client.completeHandshake(peerID)
	if err != nil {
		conn.Close()
		return nil, err
	}

	err = client.recvBitfield()
	if err != nil {
		conn.Close()
		return nil, err
	}

	if len(*bitfield) != bytes.Count(*bitfield, []byte{0}) {
		err = client.SendBitfield(bitfield)
		if err != nil {
			return nil, err
		}
	}

	return &client, nil
}

// New connects with a peer, completes a handshake, and receives a handshake
// returns an err if any of those fail
func newMagnetClient(peer Peer, peerID, infoHash [20]byte) (*PeerConnection, error) {
	conn, err := net.DialTimeout("tcp", peer.String(), 1*time.Second)
	if err != nil {
		return nil, err
	}
	client := PeerConnection{
		Conn:     conn,
		AmChoked: true,
		InfoHash: infoHash,
	}
	_, err = completeMagnetHandshake(client.Conn, infoHash, peerID)
	if err != nil {
		conn.Close()
		return nil, err
	}

	err = client.recvBitfield()
	if err != nil {
		conn.Close()
		return nil, err
	}

	return &client, nil
}

// Read reads and consumes a message from the connection
func (c *PeerConnection) Read() (*Message, error) {
	msg, err := readMessage(c, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return msg, err
}

// ReadMessages reads and consumes a message from the connection
func (c *PeerConnection) ReadMessages() error {
	for {
		msg, err := readMessage(c, 1*time.Second)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				return nil
			}
			fmt.Println("error reading messages:", err)
			return err
		}
		err = c.handleMessage(msg)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *PeerConnection) handleMessage(msg *Message) error {
	if msg == nil { // keep-alive
		return nil
	}

	switch msg.ID {
	case MsgChoke:
		c.AmChoked = true
	case MsgUnchoke:
		c.AmChoked = false
	case MsgInterested:
		fmt.Println(msg.String())
		c.PeerInterested = true
		c.SendUnchoke()
	case MsgNotInterested:
		c.PeerInterested = false
	case MsgHave:
		index, err := parseHave(msg)
		if err != nil {
			return err
		}
		c.PeerBitfield.SetPiece(index)
	case MsgBitfield:
		c.PeerBitfield = msg.Payload
	case MsgRequest:
		fmt.Println(msg.String())
		err := c.HandleRequest(msg)
		if err != nil {
			return err
		}
	case MsgExtended:
		c.handleExtension(msg.Payload)
	case MsgPiece:
		n, err := parsePiece(c.PieceState.index, c.PieceState.buf, msg)
		if err != nil {
			return err
		}
		c.PieceState.downloaded += n
		c.PieceState.backlog--

	default:
		return fmt.Errorf("unrecognized message ID %d", msg.ID)
	}
	return nil
}

func (c *PeerConnection) HandleRequest(m *Message) error {
	index, begin, length, err := parseRequest(m)
	if err != nil {
		return err
	}
	fmt.Println(index, begin, length)
	return nil
}

// SendRequest sends a Request message to the peer
func (c *PeerConnection) SendRequest(index, begin, length int) error {
	req := formatRequest(index, begin, length)
	_, err := c.Conn.Write(req.Serialize())
	return err
}

// SendCancel sends a Cancel message to the peer
func (c *PeerConnection) SendCancel(index, begin, length int) error {
	req := formatCancel(index, begin, length)
	_, err := c.Conn.Write(req.Serialize())
	return err
}

// SendBitfield sends a Request message to the peer
func (c *PeerConnection) SendBitfield(bitfield *Bitfield) error {
	req := formatBitfield(*bitfield)
	_, err := c.Conn.Write(req.Serialize())
	return err
}

// SendInterested sends an AmInterested message to the peer
func (c *PeerConnection) SendInterested() error {
	msg := Message{ID: MsgInterested}
	_, err := c.Conn.Write(msg.Serialize())
	return err
}

// SendNotInterested sends a NotInterested message to the peer
func (c *PeerConnection) SendNotInterested() error {
	msg := Message{ID: MsgNotInterested}
	_, err := c.Conn.Write(msg.Serialize())
	return err
}

// SendUnchoke sends an Unchoke message to the peer
func (c *PeerConnection) SendUnchoke() error {
	msg := Message{ID: MsgUnchoke}
	_, err := c.Conn.Write(msg.Serialize())
	return err
}

// SendHave sends a Have message to the peer
func (c *PeerConnection) SendHave(index int) error {
	msg := formatHave(index)
	_, err := c.Conn.Write(msg.Serialize())
	return err
}
