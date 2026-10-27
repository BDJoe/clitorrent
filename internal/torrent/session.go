package torrent

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"gotorrent/internal/util"
	"io"
	"log"
	"math"
	rand2 "math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// MaxBlockSize is the largest number of bytes a request can ask for
const MaxBlockSize = 16384

// MaxBackLog is the number of unfulfilled requests a client can have in its pipeline
const MaxBackLog = 5

const MaxConnections = 50

const MaxActivePieces = 10

// Session holds data required to download a torrent from a list of peers
type Session struct {
	TrackerInfo
	ConnectedPeers
	Seeders            uint32
	Leechers           uint32
	PeerID             [20]byte
	PieceHashes        [][20]byte
	PieceLength        int
	Length             int
	Name               string
	Files              []TorrentFile
	Path               string
	PiecesDone         map[int]*pieceResult
	PiecesNeeded       []*pieceWork
	Tui                *tea.Program
	TorrentID          int
	closeChan          chan struct{}
	workQueue          chan *pieceWork
	activePieces       chan *pieceWork
	results            chan *pieceResult
	bitfield           Bitfield
	messageReceiveChan chan PeerMessage
	addConnectionChan  chan *PeerConnection
	isMagnet           bool
	haveMetadata       bool
}

type ConnectedPeers struct {
	peers map[string]*PeerConnection
	mx    sync.Mutex
}

type pieceWork struct {
	index  int
	hash   [20]byte
	length int
}

type pieceResult struct {
	index int
	buf   []byte
}

type pieceProgress struct {
	index      int
	buf        []byte
	downloaded int
	requested  int
	backlog    int
}

func newSession(tf TorrentInfo) (*Session, error) {
	var peerID [20]byte
	_, err := rand.Read(peerID[:])
	if err != nil {
		return nil, err
	}

	tracker := TrackerInfo{Announce: tf.Announce, AnnounceList: tf.AnnounceList, InfoHash: tf.InfoHash, Length: tf.Length}

	bf := newBitfield(len(tf.PieceHashes))

	session := Session{
		TrackerInfo:  tracker,
		PeerID:       peerID,
		PieceHashes:  tf.PieceHashes,
		PieceLength:  tf.PieceLength,
		Length:       tf.Length,
		Name:         tf.Name,
		Files:        tf.Files,
		bitfield:     bf,
		PiecesDone:   make(map[int]*pieceResult),
		closeChan:    make(chan struct{}),
		isMagnet:     false,
		haveMetadata: true,
	}
	return &session, nil
}

func newSessionFromMagnet(tf TorrentInfo) (*Session, error) {
	var peerID [20]byte
	_, err := rand.Read(peerID[:])
	if err != nil {
		return nil, err
	}
	track := TrackerInfo{Announce: "", AnnounceList: tf.AnnounceList, InfoHash: tf.InfoHash}
	session := Session{
		TrackerInfo:  track,
		PeerID:       peerID,
		closeChan:    make(chan struct{}),
		Name:         tf.Name,
		PiecesDone:   make(map[int]*pieceResult),
		isMagnet:     true,
		haveMetadata: false,
	}
	return &session, nil
}

func attemptDownloadPiece(c *PeerConnection, pw *pieceWork) ([]byte, error) {
	state := pieceProgress{
		index: pw.index,
		buf:   make([]byte, pw.length),
	}

	c.PieceState = &state

	// Setting a deadline helps get unresponsive peers unstuck.
	// 30 seconds is more than enough time to download a 262 KB piece
	c.Conn.SetDeadline(time.Now().Add(30 * time.Second))
	defer c.Conn.SetDeadline(time.Time{}) // Disable the deadline

	for state.downloaded < pw.length {
		if c.AmChoked {
			c.SendInterested()
			continue
		}
		// If unchoked, send requests until we have enough unfulfilled requests
		if !c.AmChoked {
			for state.backlog < MaxBackLog && state.requested < pw.length {
				blockSize := MaxBlockSize
				// Last block might be shorter than the typical block
				if pw.length-state.requested < blockSize {
					blockSize = pw.length - state.requested
				}

				c.SendRequest(pw.index, state.requested, blockSize)
				state.backlog++
				state.requested += blockSize
			}
		}
	}

	return state.buf, nil
}

func checkIntegrity(pw *pieceWork, buf []byte) error {
	hash := sha1.Sum(buf)
	if !bytes.Equal(hash[:], pw.hash[:]) {
		return fmt.Errorf("Index %d failed integrity check", pw.index)
	}
	return nil
}

func (s *Session) startPeerConnection(peer Peer) {
	conn, err := newClient(peer, s.InfoHash, s.PeerID, &s.bitfield)
	if err != nil {
		if err.Error() != io.EOF.Error() {
			//s.Tui.Send(util.ErrorMsg{TorrentId: s.TorrentID, Err: err.Error()})
		}

		return
	}
	s.addConnection(conn)
}

func (s *Session) addConnection(conn *PeerConnection) {
	if len(s.addConnectionChan) < cap(s.addConnectionChan) {
		select {
		case s.addConnectionChan <- conn:
		case <-s.closeChan:
			conn.Conn.Close()
		}
	} else {
		conn.Conn.Close()
	}
}

func (s *Session) handleDownload(conn *PeerConnection) {
	for {
		if !s.haveMetadata {
			if conn.SupportedExtensions == nil {
				time.Sleep(1 * time.Second)
				continue
			}
			metadata, err := conn.getMetadata()
			if err != nil {
				return
			}
			metadataHash := sha1.Sum(metadata)

			if !bytes.Equal(metadataHash[:], s.InfoHash[:]) {
				return
			}

			info, err := ParseTorrentMagnet(metadata)
			if err != nil {
				return
			}
			s.PieceHashes = info.PieceHashes
			s.PieceLength = info.PieceLength
			s.Files = info.Files
			s.Length = info.Length
			bf := newBitfield(len(info.PieceHashes))
			s.bitfield = bf
			err = s.initFile()
			if err != nil {
				return
			}
			err = s.createCache()
			if err != nil {
				return
			}
			s.haveMetadata = true
			s.workQueue = make(chan *pieceWork, len(s.PieceHashes))
			s.scheduleWork()
			time.Sleep(1 * time.Second)
			continue
		}
		if len(s.PiecesDone) == len(s.PieceHashes) {
			//s.removePeer(conn)
			return
		}
		if conn.AmChoked {
			time.Sleep(5 * time.Second)
			continue
		}
		select {
		case <-s.closeChan:
			s.removePeer(conn)
			return
		case piece := <-s.activePieces:
			if !conn.PeerBitfield.HasPiece(piece.index) {
				s.workQueue <- piece // Put piece back on the queue
				continue
			}
			// Download the piece
			buf, err := attemptDownloadPiece(conn, piece)
			if err != nil {
				//log.Println("Exiting", err)
				s.workQueue <- piece
				continue
			}

			err = checkIntegrity(piece, buf)
			if err != nil {
				s.workQueue <- piece
				continue
			}

			s.bitfield.SetPiece(piece.index)
			s.results <- &pieceResult{piece.index, buf}
		default:
		}
	}
}

func (s *Session) removePeer(conn *PeerConnection) {
	if conn.Conn != nil {
		conn.Conn.Close()
	}
	s.ConnectedPeers.mx.Lock()
	if _, ok := s.ConnectedPeers.peers[conn.Address.String()]; ok {
		delete(s.ConnectedPeers.peers, conn.Address.String())
	}
	s.ConnectedPeers.mx.Unlock()
}

func (s *Session) runConnection(conn *PeerConnection) {
	if conn.PeerID == s.PeerID {
		conn.Conn.Close()
		return
	}

	for _, peer := range s.ConnectedPeers.peers {
		if peer.PeerID == conn.PeerID {
			// Peer with this id is already running
			conn.Conn.Close()
			return
		}
	}

	if len(s.ConnectedPeers.peers) >= MaxConnections {
		// already have enough peer connections
		conn.Conn.Close()
		return
	}

	//if !s.setInterested(conn) {
	//	conn.Conn.Close()
	//	return
	//}
	s.ConnectedPeers.mx.Lock()
	s.ConnectedPeers.peers[conn.Address.String()] = conn
	s.ConnectedPeers.mx.Unlock()
	go conn.peerListener(s.messageReceiveChan)
	go s.handleDownload(conn)
}

//func (s *Session) handleMessages(c *PeerConnection) {
//	for {
//		msg, err := c.Read()
//		if err != nil {
//			//if err.Error() != io.EOF.Error() {
//			//	s.Tui.Send(util.ErrorMsg{TorrentId: s.TorrentID, Err: err.Error()})
//			//}
//			continue
//		}
//		err = c.handleMessage(msg)
//		if err != nil {
//			continue
//		}
//	}
//}

//func (s *Session) handleSeeding() {
//	err := s.TrackerInfo.sendAnnounce(EventCompleted, s)
//	if err != nil {
//		return
//	}
//	ln, err := net.Listen("tcp", ":6881")
//	defer ln.Close()
//	if err != nil {
//		return
//	}
//	for {
//		select {
//		case <-s.closeChan:
//			ln.Close()
//			return
//		default:
//			conn, err := ln.Accept()
//			if err != nil {
//				conn.Close()
//				continue
//			}
//			client := PeerConnection{
//				Conn:           conn,
//				AmChoked:       true,
//				PeerChoked:     true,
//				AmInterested:   false,
//				PeerInterested: false,
//				InfoHash:       s.InfoHash,
//			}
//			res, err := client.completeHandshake(s.PeerID)
//			if err != nil {
//				conn.Close()
//				continue
//			}
//			client.PeerID = res.PeerID
//
//			err = client.recvBitfield()
//			if err != nil {
//				conn.Close()
//				continue
//			}
//
//			err = client.SendBitfield(&s.bitfield)
//			if err != nil {
//				return
//			}
//
//			go client.peerListener(s.peerMessageChan)
//		}
//	}
//}

func (s *Session) calculateBoundsForPiece(index int) (begin int, end int) {
	begin = index * s.PieceLength
	end = begin + s.PieceLength
	if end > s.Length {
		end = s.Length
	}
	return begin, end
}

func (s *Session) calculatePieceSize(index int) int {
	begin, end := s.calculateBoundsForPiece(index)
	return end - begin
}

func (s *Session) sendHaveToClients(pieceIndex int) {
	for _, client := range s.ConnectedPeers.peers {
		client.SendHave(pieceIndex)
	}
}

func (s *Session) StartSession(program *tea.Program, id int) error {

	program.Send(util.StatusMsg{TorrentId: id, Status: "Connecting to peers"})
	err := GetPeers(&s.TrackerInfo, s.PeerID, EventStarted)
	if err != nil {
		return err
	}

	//if s.isMagnet {
	//	program.Send(util.StatusMsg{TorrentId: id, Status: "Getting Metadata"})
	//	m, err := GetMetadata(s.TrackerInfo.Peers, s.PeerID, s.InfoHash)
	//	if err != nil {
	//		return err
	//	}
	//	s.PieceHashes = m.PieceHashes
	//	s.PieceLength = m.PieceLength
	//	s.Length = m.Length
	//	s.Files = m.Files
	//	err = s.initFile()
	//	if err != nil {
	//		return err
	//	}
	//	err = s.createCache()
	//	if err != nil {
	//		return err
	//	}
	//}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		err = s.RunSession(program, id)
		wg.Done()
	}()
	wg.Wait()
	if err != nil {
		return err
	}

	program.Send(util.StatusMsg{TorrentId: id, Status: "Ready"})
	return nil
}

func (s *Session) StopDownload() {
	s.closeChan <- struct{}{}
}

func (s *Session) connectToPeers() {
	// Start workers
	for {
		//s.ConnectedPeers.mx.Lock()
		for _, peer := range s.Peers {
			if len(s.ConnectedPeers.peers) < MaxConnections {
				if _, ok := s.ConnectedPeers.peers[peer.String()]; !ok {
					s.startPeerConnection(peer)
				}
			}
		}
		time.Sleep(5 * time.Minute)
		//s.ConnectedPeers.mx.Unlock()
	}
}

func (s *Session) RunSession(program *tea.Program, id int) error {
	// Init queues for workers to retrieve work and send results
	s.ConnectedPeers.peers = make(map[string]*PeerConnection)
	s.messageReceiveChan = make(chan PeerMessage)
	s.addConnectionChan = make(chan *PeerConnection, MaxConnections)
	s.activePieces = make(chan *pieceWork, MaxActivePieces)
	s.results = make(chan *pieceResult)
	s.PiecesNeeded = make([]*pieceWork, 0)
	if s.haveMetadata {
		s.workQueue = make(chan *pieceWork, len(s.PieceHashes))
		s.scheduleWork()
	}

	go s.connectToPeers()
	//seeding := false

outer:
	for {
		//if len(s.PiecesDone) == len(s.PieceHashes) {
		//	break outer
		//}
		//if len(s.ConnectedPeers.peers) < MaxConnections &&
		//	len(s.ConnectedPeers.peers) < len(s.Peers) {
		//	s.connectToPeers()
		//}
		var status string
		if s.haveMetadata {
			status = "Downloading"
		} else {
			status = "Getting metadata"
		}
		s.Tui.Send(util.StatusMsg{TorrentId: s.TorrentID, Status: fmt.Sprintf("Peers: %d(%d) - %s", len(s.ConnectedPeers.peers), len(s.Peers), status)})
		select {
		case conn := <-s.addConnectionChan:
			s.runConnection(conn)

		case piece := <-s.workQueue:
			if len(s.activePieces) >= MaxActivePieces {
				s.workQueue <- piece
				continue
			}
			s.activePieces <- piece

		case msg := <-s.messageReceiveChan:
			var err error
			//if !s.haveMetadata {
			//	err = msg.peer.HandleExtension(msg.message)
			//} else {
			//	err = s.handleMessage(msg.message, msg.peer)
			//}
			err = s.handleMessage(msg.message, msg.peer)
			if err != nil {
				break
			}

		case res := <-s.results:
			begin, _ := s.calculateBoundsForPiece(res.index)
			s.saveFile(res.buf, begin)
			//donePieces++
			s.PiecesDone[res.index] = res
			//err := t.saveCache()
			//if err != nil {
			//	program.Send(util.ErrorMsg{TorrentId: id, Err: err.Error()})
			//}
			s.sendHaveToClients(res.index)
			program.Send(util.ProgressMsg{TorrentId: id, Progress: getCompletePercentage(len(s.PiecesDone), len(s.PieceHashes))})
			if len(s.PiecesDone) == len(s.PieceHashes) {
				//seeding = true
				s.closeChan <- struct{}{}
			}

		case <-s.closeChan:
			//close(s.workQueue)
			break outer
		}
	}
	close(s.workQueue)
	close(s.activePieces)
	//if seeding {
	//	s.handleSeeding()
	//}
	return nil
}

//func (s *Session) requestPiece(c *PeerConnection) {
//	for piece := range s.activePieces {
//		if c.PeerBitfield.HasPiece(piece.index) {
//			attemptDownloadPiece(c, piece)
//		}
//	}
//}

func (s *Session) handleMessage(msg *Message, c *PeerConnection) error {
	if msg == nil { // keep-alive
		return nil
	}

	logFile, err := os.OpenFile("app.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer logFile.Close()

	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	switch msg.ID {
	case MsgChoke:
		c.AmChoked = true
	case MsgUnchoke:
		c.AmChoked = false
	case MsgInterested:
		log.Println("RECEIVED: Message Type: ", msg.ID, "Peer :", c.Address)
		c.PeerInterested = true
		c.PeerChoked = false
		c.SendUnchoke()
	case MsgNotInterested:
		c.PeerInterested = false
		c.PeerChoked = true
	case MsgHave:
		index, err := parseHave(msg)
		if err != nil {
			return err
		}
		c.PeerBitfield.SetPiece(index)
		s.setInterested(c)
	case MsgBitfield:
		c.PeerBitfield = msg.Payload
		if !s.setInterested(c) {
			c.SendNotInterested()
		}

	case MsgRequest:
		log.Println("RECEIVED: Message Type: ", msg.ID, "Peer :", c.Address)
		pieceMsg, err := s.formatPieceMessage(msg)
		if err != nil {
			return err
		}
		c.SendPiece(pieceMsg)
	case MsgExtended:
		c.processExtension(msg.Payload)
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

func (s *Session) setInterested(c *PeerConnection) bool {
	for _, piece := range s.PiecesNeeded {
		if c.PeerBitfield.HasPiece(piece.index) {
			c.AmInterested = true
			c.SendInterested()
			return true
		}
	}
	return false
}

func (s *Session) formatPieceMessage(msg *Message) (*Message, error) {
	index, begin, length, err := parseRequest(msg)
	if err != nil {
		return nil, err
	}
	parsedIndex := int(binary.BigEndian.Uint32(msg.Payload[0:4]))
	if parsedIndex != index {
		return nil, fmt.Errorf("Expected index %d, got %d", index, parsedIndex)
	}
	if piece, ok := s.PiecesDone[index]; ok {
		if begin >= len(piece.buf) {
			return nil, fmt.Errorf("Begin offset too high. %d >= %d", begin, len(piece.buf))
		}
		data := piece.buf[begin : begin+length]
		if begin+len(data) > len(piece.buf) {
			return nil, fmt.Errorf("Data too long [%d] for offset %d with length %d", len(data), begin, len(piece.buf))
		}
		buf := make([]byte, length)
		copy(buf, data)
		pieceMsg := formatPiece(piece.index, begin, buf)
		return pieceMsg, nil
	}
	return nil, fmt.Errorf("Piece not found for message ID %d", msg.ID)
}

func (s *Session) scheduleWork() {
	for index, hash := range s.PieceHashes {
		if _, ok := s.PiecesDone[index]; !ok {
			length := s.calculatePieceSize(index)
			s.PiecesNeeded = append(s.PiecesNeeded, &pieceWork{index: index, length: length, hash: hash})
		} else {
			s.bitfield.SetPiece(index)
		}
	}
	rand2.Shuffle(len(s.PiecesNeeded), func(i, j int) {
		s.PiecesNeeded[i], s.PiecesNeeded[j] = s.PiecesNeeded[j], s.PiecesNeeded[i]
	})
	for _, piece := range s.PiecesNeeded {
		s.workQueue <- piece
	}
}

func getCompletePercentage(done int, total int) float64 {
	res := math.Min(float64(done)/float64(total), 0.99)
	if done == total {
		res = 1.0
	}
	return res
}

func (s *Session) getCache(buf []byte) error {
	for index, hash := range s.PieceHashes {
		begin, end := s.calculateBoundsForPiece(index)
		if len(buf) >= end {
			bufHash := sha1.Sum(buf[begin:end])
			if !bytes.Equal(bufHash[:], hash[:]) {
				continue
			}
			s.PiecesDone[index] = &pieceResult{
				index: index,
				buf:   buf[begin:end],
			}
			s.bitfield.SetPiece(index)
		}
	}
	return nil
}

func (s *Session) initFile() error {
	var buf []byte
	var err error
	if len(s.Files) > 0 {
		buf, err = s.initMultiFile()
		if err != nil {
			return err
		}
	} else {
		buf, err = s.initSingleFile()
		if err != nil {
			return err
		}
	}
	if buf != nil {
		err = s.getCache(buf)
		if err != nil {
			return err
		}
		s.TrackerInfo.Left = s.Length - len(buf)
	}
	return nil
}

func (s *Session) initSingleFile() ([]byte, error) {
	if !util.Exists(s.Path) {
		util.MakeDir(s.Path)
	}
	fullPath := filepath.Join(s.Path, s.Name)

	if !util.Exists(fullPath) {
		file, err := os.Create(fullPath)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		return nil, nil
	}

	fileBuf, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, err
	}
	return fileBuf, err
}

func (s *Session) initMultiFile() ([]byte, error) {
	basePath := filepath.Join(s.Path, s.Name)
	if !util.Exists(basePath) {
		util.MakeDir(basePath)
		var torrentFile TorrentFile
		for _, torrentFile = range s.Files {
			filePath := filepath.Join(torrentFile.Path...)
			d, f := filepath.Split(filePath)
			if !util.Exists(filepath.Join(basePath, d)) {
				util.MakeDir(filepath.Join(basePath, d))
			}
			fullPath := filepath.Join(basePath, d, f)
			file, err := os.Create(fullPath)
			if err != nil {
				return nil, err
			}
			file.Close()
		}
		return nil, nil
	}
	var torrentFile TorrentFile
	var buf []byte
	for _, torrentFile = range s.Files {
		filePath := filepath.Join(torrentFile.Path...)
		d, f := filepath.Split(filePath)
		fullPath := filepath.Join(basePath, d, f)
		if !util.Exists(fullPath) {
			f, err := os.Create(fullPath)
			if err != nil {
				return nil, err
			}
			f.Close()
		}
		file, err := os.ReadFile(fullPath)

		if err != nil {
			return nil, err
		}

		buf = append(buf, file...)
	}
	return buf, nil
}

func (s *Session) saveFile(buf []byte, begin int) error {
	if len(s.Files) > 0 {
		err := s.saveMultiFile(buf, begin)
		if err != nil {
			return err
		}
	} else {
		err := s.saveSingleFile(buf, begin)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) saveSingleFile(buf []byte, begin int) error {
	if !util.Exists(s.Path) {
		util.MakeDir(s.Path)
	}
	fullPath := filepath.Join(s.Path, s.Name)
	var outFile *os.File
	var err error
	if !util.Exists(fullPath) {
		outFile, err = os.Create(fullPath)
		if err != nil {
			return err
		}
	} else {
		outFile, err = os.OpenFile(fullPath, os.O_RDWR, 0666)
		if err != nil {
			return err
		}
	}
	defer outFile.Close()
	_, err = outFile.WriteAt(buf, int64(begin))
	if err != nil {
		return err
	}

	return nil
}

func (s *Session) saveMultiFile(buf []byte, begin int) error {
	bytesWritten := 0
	for len(buf) > 0 {
		startPos := begin + bytesWritten
		bytesToWrite := len(buf)
		lengthCounter := 0
		var torrentFile TorrentFile
		for _, torrentFile = range s.Files {
			fileEnd := lengthCounter + torrentFile.Length
			if startPos < fileEnd {
				if len(buf)+startPos > fileEnd {
					bytesToWrite = fileEnd - startPos
				}
				break
			}
			lengthCounter += torrentFile.Length
		}

		filePath := filepath.Join(torrentFile.Path...)
		d, f := filepath.Split(filePath)
		dir := filepath.Join(s.Path, s.Name, d)
		fullPath := filepath.Join(dir, f)

		if !util.Exists(dir) {
			util.MakeDir(dir)
		}

		var outFile *os.File
		var err error
		if !util.Exists(fullPath) {
			outFile, err = os.Create(fullPath)
			if err != nil {
				return err
			}
		} else {
			outFile, err = os.OpenFile(fullPath, os.O_RDWR, 0666)
			if err != nil {
				return err
			}
		}
		defer outFile.Close()
		_, err = outFile.WriteAt(buf[:bytesToWrite], int64(startPos)-int64(lengthCounter))
		if err != nil {
			return err
		}
		buf = buf[bytesToWrite:]
		bytesWritten += bytesToWrite
	}
	return nil
}
