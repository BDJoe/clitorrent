package torrent

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

// A Client is a TCP connection with a peer
type Client struct {
	Conn           net.Conn
	Choked         bool
	PeerChoked     bool
	Interested     bool
	PeerInterested bool
	Bitfield       *Bitfield
	PeerBitfield   Bitfield
	Extension
	Metadata
	InfoHash [20]byte
	MyPeerID [20]byte
	//PeerID   [20]byte
	Peer Peer
}

func (c *Client) completeHandshake() (*Handshake, error) {
	c.Conn.SetDeadline(time.Now().Add(3 * time.Second))
	defer c.Conn.SetDeadline(time.Time{}) // Disable the deadline

	req := newHandshake(c.InfoHash, c.MyPeerID)
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

	if len(*c.Bitfield) != 0 {
		err = c.SendBitfield()
		if err != nil {
			return nil, err
		}
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

func (c *Client) recvBitfield() error {
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
func newClient(peer Peer, peerID, infoHash [20]byte, bitfield *Bitfield) (*Client, error) {
	conn, err := net.DialTimeout("tcp", peer.String(), 3*time.Second)
	if err != nil {
		return nil, err
	}
	client := Client{
		Conn:           conn,
		Choked:         true,
		PeerChoked:     true,
		Interested:     false,
		PeerInterested: false,
		InfoHash:       infoHash,
		MyPeerID:       peerID,
		Bitfield:       bitfield,
	}
	_, err = client.completeHandshake()
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

// New connects with a peer, completes a handshake, and receives a handshake
// returns an err if any of those fail
func newMagnetClient(peer Peer, peerID, infoHash [20]byte) (*Client, error) {
	conn, err := net.DialTimeout("tcp", peer.String(), 1*time.Second)
	if err != nil {
		return nil, err
	}
	client := Client{
		Conn:     conn,
		Choked:   true,
		InfoHash: infoHash,
		MyPeerID: peerID,
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
func (c *Client) Read() (*Message, error) {
	msg, err := readMessage(c, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return msg, err
}

// ReadMessages reads and consumes a message from the connection
func (c *Client) ReadMessages() error {
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

func (c *Client) handleMessage(msg *Message) error {
	if msg == nil { // keep-alive
		return nil
	}

	switch msg.ID {
	case MsgChoke:
		c.Choked = true
	case MsgUnchoke:
		c.Choked = false
	case MsgInterested:
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
		err := c.HandleRequest(msg)
		if err != nil {
			return err
		}
	case MsgExtended:
		c.handleExtension(msg.Payload)
	default:
		return fmt.Errorf("unrecognized message ID %d", msg.ID)
	}
	return nil
}

func (c *Client) HandleRequest(m *Message) error {
	index, begin, length, err := parseRequest(m)
	if err != nil {
		return err
	}
	fmt.Println(index, begin, length)
	return nil
}

// SendRequest sends a Request message to the peer
func (c *Client) SendRequest(index, begin, length int) error {
	req := formatRequest(index, begin, length)
	_, err := c.Conn.Write(req.Serialize())
	return err
}

// SendBitfield sends a Request message to the peer
func (c *Client) SendBitfield() error {
	req := formatBitfield(*c.Bitfield)
	_, err := c.Conn.Write(req.Serialize())
	return err
}

// SendInterested sends an Interested message to the peer
func (c *Client) SendInterested() error {
	msg := Message{ID: MsgInterested}
	_, err := c.Conn.Write(msg.Serialize())
	return err
}

// SendNotInterested sends a NotInterested message to the peer
func (c *Client) SendNotInterested() error {
	msg := Message{ID: MsgNotInterested}
	_, err := c.Conn.Write(msg.Serialize())
	return err
}

// SendUnchoke sends an Unchoke message to the peer
func (c *Client) SendUnchoke() error {
	msg := Message{ID: MsgUnchoke}
	_, err := c.Conn.Write(msg.Serialize())
	return err
}

// SendHave sends a Have message to the peer
func (c *Client) SendHave(index int) error {
	msg := formatHave(index)
	_, err := c.Conn.Write(msg.Serialize())
	return err
}
