package torrentFile

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"gotorrent/internal/peers"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackpal/bencode-go"
)

type bencodeTrackerResp struct {
	Interval int    `bencode:"interval"`
	Peers    string `bencode:"peers"`
}

type AnnounceRequest struct {
	connectionId  uint64
	transactionId uint32
	InfoHash      [20]byte
	PeerId        [20]byte
	Downloaded    uint64
	Left          uint64
	Uploaded      uint64
	Event         uint32
	Ipaddr        uint32
	Key           uint32
	NumWant       int32
	Port          uint16
}

type AnnounceResponse struct {
	action        uint32
	transactionId uint32
	interval      uint32
	leechers      uint32
	seeders       uint32
	Peers         []peers.Peer
}

func (t *TorrentInfo) buildTrackerURL(announce string, peerID [20]byte, port uint16) (string, error) {
	base, err := url.Parse(announce)
	if err != nil {
		return "", err
	}
	params := url.Values{
		"info_hash":  []string{string(t.InfoHash[:])},
		"peer_id":    []string{string(peerID[:])},
		"port":       []string{strconv.Itoa(int(port))},
		"uploaded":   []string{"0"},
		"downloaded": []string{"0"},
		"compact":    []string{"1"},
		"left":       []string{strconv.Itoa(t.Length)},
	}
	base.RawQuery = params.Encode()
	return base.String(), nil
}

func (t *TorrentInfo) requestPeers(announce string, peerID [20]byte, port uint16) ([]peers.Peer, error) {
	url, err := url.Parse(announce)
	if err != nil {
		return nil, err
	}
	peers := []peers.Peer{}

	switch url.Scheme {
	case "http":
		peers, err = t.requestPeersHTML(announce, peerID, port)
		if err != nil {
			return nil, err
		}
	case "udp":
		peers, err = t.requestPeersUDP(announce, peerID, port)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("Invalid Announce Scheme")
	}

	return peers, nil
}

func (t *TorrentInfo) requestPeersHTML(announce string, peerID [20]byte, port uint16) ([]peers.Peer, error) {
	url, err := t.buildTrackerURL(announce, peerID, port)
	if err != nil {
		return nil, err
	}

	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	trackerResp := bencodeTrackerResp{}

	err = bencode.Unmarshal(resp.Body, &trackerResp)
	if err != nil {
		return nil, err
	}

	return peers.Unmarshal([]byte(trackerResp.Peers))
}

func (t *TorrentInfo) requestPeersUDP(announce string, peerID [20]byte, port uint16) ([]peers.Peer, error) {
	address, err := url.Parse(announce)
	if err != nil {
		return nil, err
	}
	//fmt.Println(address.Host, address.Scheme)

	udpAddr, err := net.ResolveUDPAddr("udp", address.Host)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, err
	}
	req, err := createConnectionReq()
	if err != nil {
		return nil, err
	}
	//fmt.Printf("connection packet: % x\n", req)
	_, err = conn.Write(req[:])
	if err != nil {
		return nil, err
	}

	conn.SetDeadline(time.Now().Add(time.Second * 5))
	buf := make([]byte, 32)
	n, err := bufio.NewReader(conn).Read(buf)
	conn.SetDeadline(time.Time{})
	if err != nil {
		return nil, err
	}

	_ = binary.BigEndian.Uint32(buf)
	transId := binary.BigEndian.Uint32(buf[4:])
	connId := binary.BigEndian.Uint64(buf[8:])

	//fmt.Printf("n: %v, action: % x, received tid: % x, conn Id: % x\n", n, action, transId, connId)

	r := AnnounceRequest{
		InfoHash:      t.InfoHash,
		Left:          1,
		NumWant:       -1,
		Port:          port,
		connectionId:  connId,
		transactionId: transId,
		PeerId:        peerID,
	}

	a := createAnnounceReq(r)

	_, err = conn.Write(a[:])

	conn.SetDeadline(time.Now().Add(time.Second * 10))

	newBuf := make([]byte, 1024)
	n, err = bufio.NewReader(conn).Read(newBuf)
	if err != nil {
		return nil, err
	}
	//fmt.Printf("Received %v bytes for announce\n", n)
	res, err := parseAnnounceResponse(newBuf, n)
	if err != nil {
		return nil, err
	}
	return res.Peers, nil
}

func createConnectionReq() ([16]byte, error) {
	buf := [16]byte{}

	magicCode, err := hex.DecodeString("0000041727101980")
	if err != nil {
		return buf, err
	}

	copy(buf[:], magicCode)

	binary.BigEndian.PutUint32(buf[12:], rand.Uint32())

	return buf, nil
}

func createAnnounceReq(r AnnounceRequest) []byte {
	buf := make([]byte, 98)
	binary.BigEndian.PutUint64(buf, r.connectionId) // 8 bytes
	// Action - 1 for Announce
	binary.BigEndian.PutUint32(buf[8:], 1)                // 4 bytes
	binary.BigEndian.PutUint32(buf[12:], r.transactionId) // 4 bytes
	copy(buf[16:], r.InfoHash[:])                         // 20 bytes
	copy(buf[36:], r.PeerId[:])                           // 20 bytes
	binary.BigEndian.PutUint64(buf[56:], r.Downloaded)    // 8 bytes
	binary.BigEndian.PutUint64(buf[64:], r.Left)          // 8 bytes
	binary.BigEndian.PutUint64(buf[72:], r.Uploaded)      // 8 bytes
	// Event
	// none = 0
	// completed = 1
	// started = 2
	// stopped = 3
	binary.BigEndian.PutUint32(buf[80:], 0)                 // 4 bytes
	binary.BigEndian.PutUint32(buf[84:], r.Ipaddr)          // 4 bytes
	binary.BigEndian.PutUint32(buf[88:], r.Key)             // 4 bytes
	binary.BigEndian.PutUint32(buf[92:], uint32(r.NumWant)) // 4 bytes
	binary.BigEndian.PutUint16(buf[96:], r.Port)            // 2 bytes

	return buf
}

func parseAnnounceResponse(b []byte, l int) (AnnounceResponse, error) {
	if l < 20 {
		return AnnounceResponse{
			action: binary.BigEndian.Uint32(b[:]),
		}, nil
	}
	peerList := (l - 20) / 6

	rv := AnnounceResponse{
		action:        binary.BigEndian.Uint32(b[:]),
		transactionId: binary.BigEndian.Uint32(b[4:]),
		interval:      binary.BigEndian.Uint32(b[8:]),
		leechers:      binary.BigEndian.Uint32(b[12:]),
		seeders:       binary.BigEndian.Uint32(b[16:]),
		Peers:         []peers.Peer{},
	}

	// trackerResp := bencodeTrackerResp{}

	// err := bencode.Unmarshal(bytes.NewReader(b), &trackerResp)
	// if err != nil {
	// 	return rv, err
	// }

	// p, err := peers.Unmarshal(binary.BigEndian.Uint32(b[20:]))
	// if err != nil {
	// 	return AnnounceResponse{}, err
	// }
	// rv.Peers = p
	p := make([]peers.Peer, peerList)
	// for i := range numPeers {
	// 	offset := i * peerSize
	// 	peers[i].IP = net.IP(peersBin[offset : offset+4])
	// 	peers[i].Port = binary.BigEndian.Uint16(peersBin[offset+4 : offset+6])
	// }
	for i := 0; i < peerList; i++ {
		offset := 20 + 6*i
		p[i].IP = net.IP(b[offset : offset+4])
		p[i].Port = binary.BigEndian.Uint16(b[offset+4 : offset+6])
		//fmt.Println(p[i].IP)
	}
	rv.Peers = p
	return rv, nil
}
