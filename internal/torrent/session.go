package torrent

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"gotorrent/internal/util"
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

// Session holds data required to download a torrent from a list of peers
type Session struct {
	TrackerInfo
	Peers       []Peer
	Clients     []*Client
	Seeders     uint32
	Leechers    uint32
	PeerID      [20]byte
	PieceHashes [][20]byte
	PieceLength int
	Length      int
	Name        string
	Files       []TorrentFile
	Path        string
	PiecesDone  []int
	Tui         *tea.Program
	TorrentID   int
	closeChan   chan struct{}
	workQueue   chan *pieceWork
	results     chan *pieceResult
}

type cache struct {
	Path       string
	PiecesDone []int
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
	client     *Client
	buf        []byte
	downloaded int
	requested  int
	backlog    int
}

func (state *pieceProgress) readMessage() error {
	msg, err := state.client.Read() // this call blocks
	if err != nil {
		return err
	}

	if msg == nil { // keep-alive
		return nil
	}

	switch msg.ID {
	case MsgUnchoke:
		state.client.Choked = false
	case MsgChoke:
		state.client.Choked = true
	case MsgHave:
		index, err := parseHave(msg)
		if err != nil {
			return err
		}
		state.client.Bitfield.SetPiece(index)
	case MsgPiece:
		n, err := parsePiece(state.index, state.buf, msg)
		if err != nil {
			return err
		}
		state.downloaded += n
		state.backlog--
	}
	return nil
}

func attemptDownloadPiece(c *Client, pw *pieceWork) ([]byte, error) {
	state := pieceProgress{
		index:  pw.index,
		client: c,
		buf:    make([]byte, pw.length),
	}

	// Setting a deadline helps get unresponsive peers unstuck.
	// 30 seconds is more than enough time to download a 262 KB piece
	c.Conn.SetDeadline(time.Now().Add(30 * time.Second))
	defer c.Conn.SetDeadline(time.Time{}) // Disable the deadline

	for state.downloaded < pw.length {
		// If unchoked, send requests until we have enough unfulfilled requests
		if !state.client.Choked {
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
		err := state.readMessage()
		if err != nil {
			return nil, err
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

func (s *Session) startDownloadWorker(peer Peer, program *tea.Program, id int) {
	c, err := newClient(peer, s.PeerID, s.InfoHash)
	if err != nil {
		//log.Printf("Could not handshake with %s. Disconnecting\n", peer.IP)
		return
	}
	s.Clients = append(s.Clients, c)
	defer c.Conn.Close()
	//log.Printf("Completed handshake with %s\n", peer.IP)

	c.SendUnchoke()
	c.SendInterested()

	for pw := range s.workQueue {
		if !c.Bitfield.HasPiece(pw.index) {
			s.workQueue <- pw // Put piece back on the queue
			continue
		}
		// Download the piece
		buf, err := attemptDownloadPiece(c, pw)
		if err != nil {
			//log.Println("Exiting", err)
			s.workQueue <- pw
			return
		}

		err = checkIntegrity(pw, buf)
		if err != nil {
			program.Send(util.ErrorMsg{TorrentId: id, Err: fmt.Sprintf("Index %d failed integrity check", pw.index)})
			s.workQueue <- pw
			continue
		}

		c.SendHave(pw.index)
		s.results <- &pieceResult{pw.index, buf}
	}
}

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

func (s *Session) StartDownload(program *tea.Program, id int) error {
	if len(s.Peers) == 0 {
		program.Send(util.StatusMsg{TorrentId: id, Status: "Connecting to peers"})

		peers, err := GetPeers(&s.TrackerInfo, s.PeerID)
		if err != nil {
			return err
		}
		s.Peers = peers
	}

	var wg sync.WaitGroup
	var err error
	wg.Add(1)
	go func() {
		err = s.Download(program, id)
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

func (s *Session) Download(program *tea.Program, id int) error {
	program.Send(util.StatusMsg{TorrentId: id, Status: "Downloading"})
	// Init queues for workers to retrieve work and send results
	s.workQueue = make(chan *pieceWork, len(s.PieceHashes))
	s.results = make(chan *pieceResult)
	for index, hash := range s.PieceHashes {
		if !slices.Contains(s.PiecesDone, index) {
			length := s.calculatePieceSize(index)
			s.workQueue <- &pieceWork{index, hash, length}
		}
	}

	// Start workers
	for _, peer := range s.Peers {
		go s.startDownloadWorker(peer, program, id)
	}

	// Collect results into a buffer until full
	//donePieces := len(t.PiecesDone)
	for len(s.PiecesDone) < len(s.PieceHashes) {
		select {
		case <-s.closeChan:
			//close(s.workQueue)
			return nil

		case res := <-s.results:
			begin, _ := s.calculateBoundsForPiece(res.index)
			s.saveFile(res.buf, begin)
			//donePieces++
			s.PiecesDone = append(s.PiecesDone, res.index)
			//err := t.saveCache()
			//if err != nil {
			//	program.Send(util.ErrorMsg{TorrentId: id, Err: err.Error()})
			//}
			program.Send(util.ProgressMsg{TorrentId: id, Progress: getCompletePercentage(len(s.PiecesDone), len(s.PieceHashes))})
		}
	}
	close(s.workQueue)
	return nil
}

func getCompletePercentage(done int, total int) float64 {
	return float64(done) / float64(total)
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
