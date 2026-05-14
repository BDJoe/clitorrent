package torrent

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackpal/bencode-go"
)

type TrackerInfo struct {
	Announce     string
	AnnounceList [][]string
	InfoHash     [20]byte
	Length       int
	Peers        []Peer
	Downloaded   int
	Uploaded     int
	Left         int
}

type bencodeTrackerResp struct {
	Interval int    `bencode:"interval"`
	Peers    string `bencode:"peers"`
}

type AnnounceRequest struct {
	connectionId  uint64
	action        uint32
	transactionId uint32
	InfoHash      [20]byte
	PeerId        [20]byte
	Downloaded    uint64
	Left          uint64
	Uploaded      uint64
	Event         TrackerEvent
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
	Peers         []Peer
}

type TrackerEvent uint32

const (
	EventNone TrackerEvent = iota
	EventCompleted
	EventStarted
	EventStopped
)

// Port to listen on
const Port uint16 = 6881

func (t *TrackerInfo) sendAnnounce(event TrackerEvent, s *Session) error {
	err := GetPeers(t, s.PeerID, event)
	if err != nil {
		return err
	}
	return nil
}

func GetPeers(t *TrackerInfo, peerID [20]byte, event TrackerEvent) error {
	if len(t.AnnounceList) == 0 {
		peers, err := t.requestPeers(t.Announce, peerID, Port, event)
		if err != nil {
			return err
		}
		t.Peers = peers
		return nil
	}
	peers := make([]Peer, 0)
	for _, announce := range t.AnnounceList {
		for _, path := range announce {
			newPeers, err := t.requestPeers(path, peerID, Port, event)
			if err != nil {
				continue
			}
			peers = append(peers, newPeers...)
		}

	}

	if len(peers) == 0 {
		return fmt.Errorf("Failed to connect to trackers\n")
	}
	t.Peers = peers
	return nil
}

func (t *TrackerInfo) buildTrackerURL(announce string, peerID [20]byte, port uint16, event TrackerEvent) (string, error) {
	base, err := url.Parse(announce)
	if err != nil {
		return "", err
	}
	params := url.Values{
		"info_hash":  []string{string(t.InfoHash[:])},
		"peer_id":    []string{string(peerID[:])},
		"port":       []string{strconv.Itoa(int(port))},
		"uploaded":   []string{strconv.FormatUint(uint64(t.Uploaded), 10)},
		"downloaded": []string{strconv.FormatUint(uint64(t.Downloaded), 10)},
		"compact":    []string{"1"},
		"left":       []string{strconv.Itoa(t.Left)},
		"numwant":    []string{"-1"},
	}
	switch event {
	case EventStarted:
		params.Add("event", "started")
	case EventCompleted:
		params.Add("event", "completed")
	case EventStopped:
		params.Add("event", "stopped")
	case EventNone:
	default:
		panic("unhandled default case")
	}
	base.RawQuery = params.Encode()
	return base.String(), nil
}

func (t *TrackerInfo) requestPeers(announce string, peerID [20]byte, port uint16, event TrackerEvent) ([]Peer, error) {
	path, err := url.Parse(announce)
	if err != nil {
		return nil, err
	}
	var peers []Peer

	switch path.Scheme {
	case "http":
		peers, err = t.requestPeersHTML(announce, peerID, port, event)
		if err != nil {
			return nil, err
		}
	case "udp":
		peers, err = t.requestPeersUDP(announce, peerID, port, event)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid Announce Scheme")
	}

	return peers, nil
}

func (t *TrackerInfo) requestPeersHTML(announce string, peerID [20]byte, port uint16, event TrackerEvent) ([]Peer, error) {
	path, err := t.buildTrackerURL(announce, peerID, port, event)
	if err != nil {
		return nil, err
	}

	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get(path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	trackerResp := bencodeTrackerResp{}

	err = bencode.Unmarshal(resp.Body, &trackerResp)
	if err != nil {
		return nil, err
	}

	return unmarshalPeers([]byte(trackerResp.Peers))
}

func (t *TrackerInfo) requestPeersUDP(announce string, peerID [20]byte, port uint16, event TrackerEvent) ([]Peer, error) {
	address, err := url.Parse(announce)
	if err != nil {
		return nil, err
	}
	//fmt.Println(address.Host, address.Scheme)

	udpAddr, err := net.ResolveUDPAddr("udp", address.Host)
	if err != nil {
		return nil, err
	}

	if udpAddr.IP.String() == "127.0.0.1" {
		return nil, fmt.Errorf("UDP address is localhost")
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
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

	r := AnnounceRequest{
		InfoHash:      t.InfoHash,
		Uploaded:      0,
		Downloaded:    0,
		Left:          uint64(t.Length),
		Port:          port,
		NumWant:       -1,
		connectionId:  connId,
		transactionId: transId,
		PeerId:        peerID,
		Event:         event,
	}

	a := createAnnounceReq(r)

	_, err = conn.Write(a[:])

	conn.SetDeadline(time.Now().Add(time.Second * 5))

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
	binary.BigEndian.PutUint32(buf[8:], 1)                  // 4 bytes
	binary.BigEndian.PutUint32(buf[12:], r.transactionId)   // 4 bytes
	copy(buf[16:], r.InfoHash[:])                           // 20 bytes
	copy(buf[36:], r.PeerId[:])                             // 20 bytes
	binary.BigEndian.PutUint64(buf[56:], r.Downloaded)      // 8 bytes
	binary.BigEndian.PutUint64(buf[64:], r.Left)            // 8 bytes
	binary.BigEndian.PutUint64(buf[72:], r.Uploaded)        // 8 bytes
	binary.BigEndian.PutUint32(buf[76:], uint32(r.Event))   // Event, none = 0 completed = 1 started = 2 stopped = 3
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
		Peers:         make([]Peer, peerList),
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
	// for i := range numPeers {
	// 	offset := i * peerSize
	// 	peers[i].IP = net.IP(peersBin[offset : offset+4])
	// 	peers[i].Port = binary.BigEndian.Uint16(peersBin[offset+4 : offset+6])
	// }
	for i := 0; i < peerList; i++ {
		offset := 20 + 6*i
		rv.Peers[i].IP = net.IP(b[offset : offset+4])
		rv.Peers[i].Port = binary.BigEndian.Uint16(b[offset+4 : offset+6])
		//fmt.Println(p[i].IP)
	}
	return rv, nil
}
