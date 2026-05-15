package torrent

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"gotorrent/internal/util"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
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
	Seeders           uint32
	Leechers          uint32
	PeerID            [20]byte
	PieceHashes       [][20]byte
	PieceLength       int
	Length            int
	Name              string
	Files             []TorrentFile
	Path              string
	PiecesDone        []int
	PiecesNeeded      []*pieceWork
	Tui               *tea.Program
	TorrentID         int
	closeChan         chan struct{}
	workQueue         chan *pieceWork
	activePieces      chan *pieceWork
	results           chan *pieceResult
	bitfield          Bitfield
	peerMessageChan   chan PeerMessage
	addConnectionChan chan *PeerConnection
	isMagnet          bool
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

func attemptDownloadPiece(c *PeerConnection, pw *pieceWork) ([]byte, error) {
	state := pieceProgress{
		index: pw.index,
		buf:   make([]byte, pw.length),
	}

	c.PieceState = &state

	// Setting a deadline helps get unresponsive peers unstuck.
	// 30 seconds is more than enough time to download a 262 KB piece
	//c.Conn.SetDeadline(time.Now().Add(30 * time.Second))
	//defer c.Conn.SetDeadline(time.Time{}) // Disable the deadline

	for state.downloaded < pw.length {
		//if c.AmChoked {
		//	c.SendInterested()
		//	continue
		//}
		// If unchoked, send requests until we have enough unfulfilled requests
		if !c.AmChoked {
			for state.backlog < MaxBackLog && state.requested < pw.length {
				blockSize := MaxBlockSize
				// Last block might be shorter than the typical block
				if pw.length-state.requested < blockSize {
					blockSize = pw.length - state.requested
				}

				err := c.SendRequest(pw.index, state.requested, blockSize)
				if err != nil {
					return nil, err
				}
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
		}
	} else {
		conn.Conn.Close()
	}
}

func (s *Session) handleDownload(conn *PeerConnection) {
	for {
		if conn.AmChoked {
			time.Sleep(1 * time.Second)
			continue
		}
		if len(s.PiecesDone) == len(s.PieceHashes) {
			s.removePeer(conn)
			return
		}
		select {
		case <-s.closeChan:
			s.removePeer(conn)
			return
		case piece := <-s.activePieces:
			if !conn.PeerBitfield.HasPiece(piece.index) {
				s.workQueue <- piece // Put piece back on the queue
				break
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
	go conn.peerListener(s.peerMessageChan)
	go s.handleDownload(conn)
}

func (s *Session) handleMessages(c *PeerConnection) {
	for {
		msg, err := c.Read()
		if err != nil {
			//if err.Error() != io.EOF.Error() {
			//	s.Tui.Send(util.ErrorMsg{TorrentId: s.TorrentID, Err: err.Error()})
			//}
			continue
		}
		err = c.handleMessage(msg)
		if err != nil {
			continue
		}
	}
}

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

	if s.isMagnet {
		program.Send(util.StatusMsg{TorrentId: id, Status: "Getting Metadata"})
		m, err := GetMetadata(s.TrackerInfo.Peers, s.PeerID, s.InfoHash)
		if err != nil {
			return err
		}
		s.PieceHashes = m.PieceHashes
		s.PieceLength = m.PieceLength
		s.Length = m.Length
		s.Files = m.Files
		err = s.initFile()
		if err != nil {
			return err
		}
		err = s.createCache()
		if err != nil {
			return err
		}
	}

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
	for _, peer := range s.Peers {
		if len(s.ConnectedPeers.peers) < MaxConnections {
			if _, ok := s.ConnectedPeers.peers[peer.String()]; !ok {
				go s.startPeerConnection(peer)
			}
		}
	}
}

func (s *Session) RunSession(program *tea.Program, id int) error {
	program.Send(util.StatusMsg{TorrentId: id, Status: fmt.Sprintf("Peers: %d - Downloading", len(s.Peers))})
	// Init queues for workers to retrieve work and send results
	s.ConnectedPeers.peers = make(map[string]*PeerConnection)
	s.peerMessageChan = make(chan PeerMessage)
	s.addConnectionChan = make(chan *PeerConnection, MaxConnections)
	s.workQueue = make(chan *pieceWork, len(s.PieceHashes))
	s.activePieces = make(chan *pieceWork, MaxActivePieces)
	s.results = make(chan *pieceResult)
	s.PiecesNeeded = make([]*pieceWork, 0)
	s.scheduleWork()

	s.connectToPeers()
	//seeding := false

outer:
	for {
		s.Tui.Send(util.StatusMsg{TorrentId: s.TorrentID, Status: fmt.Sprintf("Peers: %d(%d) - Downloading", len(s.ConnectedPeers.peers), len(s.Peers))})
		select {
		case conn := <-s.addConnectionChan:
			s.runConnection(conn)

		case piece := <-s.workQueue:
			if len(s.activePieces) >= MaxActivePieces {
				s.workQueue <- piece
				continue
			}
			s.activePieces <- piece

		case msg := <-s.peerMessageChan:
			err := msg.peer.handleMessage(msg.message)
			if err != nil {
				break
			}

		case res := <-s.results:
			begin, _ := s.calculateBoundsForPiece(res.index)
			s.saveFile(res.buf, begin)
			//donePieces++
			s.PiecesDone = append(s.PiecesDone, res.index)
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

func (s *Session) requestPiece(c *PeerConnection) {
	for piece := range s.activePieces {
		if c.PeerBitfield.HasPiece(piece.index) {
			attemptDownloadPiece(c, piece)
		}
	}
}

func (s *Session) handleMessage(msg *Message, c *PeerConnection) error {
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

func (s *Session) scheduleWork() {
	for index, hash := range s.PieceHashes {
		if !slices.Contains(s.PiecesDone, index) {
			length := s.calculatePieceSize(index)
			s.PiecesNeeded = append(s.PiecesNeeded, &pieceWork{index: index, length: length, hash: hash})
		} else {
			s.bitfield.SetPiece(index)
		}
	}
	rand.Shuffle(len(s.PiecesNeeded), func(i, j int) {
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
			s.PiecesDone = append(s.PiecesDone, index)
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
		s.TrackerInfo.Left = s.Length - len(buf)
		err = s.getCache(buf)
		if err != nil {
			return err
		}
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
