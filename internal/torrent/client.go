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
	Conn     net.Conn
	Choked   bool
	Bitfield Bitfield
	Extension
	Metadata
	infoHash [20]byte
	peerID   [20]byte
}

func completeHandshake(conn net.Conn, infohash, peerID [20]byte) (*Handshake, error) {
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	defer conn.SetDeadline((time.Time{})) // Disable the deadline

	req := newHandshake(infohash, peerID)
	_, err := conn.Write(req.Serialize())
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
	c.Bitfield = msg.Payload
	return nil
}

// New connects with a peer, completes a handshake, and receives a handshake
// returns an err if any of those fail
func newClient(peer Peer, peerID, infoHash [20]byte) (*Client, error) {
	conn, err := net.DialTimeout("tcp", peer.String(), 3*time.Second)
	if err != nil {
		return nil, err
	}
	client := Client{
		Conn:     conn,
		Choked:   true,
		infoHash: infoHash,
		peerID:   peerID,
	}
	_, err = completeHandshake(client.Conn, infoHash, peerID)
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
		infoHash: infoHash,
		peerID:   peerID,
	}
	_, err = completeMagnetHandshake(client.Conn, infoHash, peerID)
	if err != nil {
		conn.Close()
		return nil, err
	}

	//bf, err := recvBitfield(conn)
	//if err != nil {
	//	conn.Close()
	//	return nil, err
	//}

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
	case MsgUnchoke:
		c.Choked = false
	case MsgChoke:
		c.Choked = true
	case MsgHave:
		index, err := parseHave(msg)
		if err != nil {
			return err
		}
		c.Bitfield.SetPiece(index)
	case MsgBitfield:
		c.Bitfield = msg.Payload
	case MsgExtended:
		c.handleExtension(msg.Payload)
	default:
		return fmt.Errorf("unrecognized message ID %d", msg.ID)
	}
	return nil
}

// SendRequest sends a Request message to the peer
func (c *Client) SendRequest(index, begin, length int) error {
	req := formatRequest(index, begin, length)
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
