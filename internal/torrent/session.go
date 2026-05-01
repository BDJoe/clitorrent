package torrent

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"gotorrent/internal/util"
	"os"
	"path/filepath"
	"slices"
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

func (t *Session) startDownloadWorker(peer Peer, workQueue chan *pieceWork, results chan *pieceResult, program *tea.Program, id int) {
	c, err := newClient(peer, t.PeerID, t.InfoHash)
	if err != nil {
		//log.Printf("Could not handshake with %s. Disconnecting\n", peer.IP)
		return
	}
	defer c.Conn.Close()
	peer.Client = c
	//log.Printf("Completed handshake with %s\n", peer.IP)

	c.SendUnchoke()
	c.SendInterested()

	for pw := range workQueue {
		if !c.Bitfield.HasPiece(pw.index) {
			workQueue <- pw // Put piece back on the queue
			continue
		}

		// Download the piece
		buf, err := attemptDownloadPiece(c, pw)
		if err != nil {
			//log.Println("Exiting", err)
			workQueue <- pw
			return
		}

		err = checkIntegrity(pw, buf)
		if err != nil {
			program.Send(util.ErrorMsg{TorrentId: id, Err: fmt.Sprintf("Index %d failed integrity check", pw.index)})
			workQueue <- pw
			continue
		}

		c.SendHave(pw.index)
		results <- &pieceResult{pw.index, buf}
	}
}

func (t *Session) calculateBoundsForPiece(index int) (begin int, end int) {
	begin = index * t.PieceLength
	end = begin + t.PieceLength
	if end > t.Length {
		end = t.Length
	}
	return begin, end
}

func (t *Session) calculatePieceSize(index int) int {
	begin, end := t.calculateBoundsForPiece(index)
	return end - begin
}

func (t *Session) StartDownload(program *tea.Program, id int) error {
	if len(t.Peers) == 0 {
		program.Send(util.StatusMsg{TorrentId: id, Status: "Connecting to peers"})

		peers, err := GetPeers(&t.TrackerInfo, t.PeerID)
		if err != nil {
			return err
		}
		t.Peers = peers
	}

	err := t.Download(program, id)
	if err != nil {
		return err
	}

	program.Send(util.StatusMsg{TorrentId: id, Status: "Download Complete!"})
	return nil
}

func (t *Session) Download(program *tea.Program, id int) error {
	program.Send(util.StatusMsg{TorrentId: id, Status: "Downloading"})
	// Init queues for workers to retrieve work and send results
	workQueue := make(chan *pieceWork, len(t.PieceHashes))
	results := make(chan *pieceResult)
	for index, hash := range t.PieceHashes {
		if !slices.Contains(t.PiecesDone, index) {
			length := t.calculatePieceSize(index)
			workQueue <- &pieceWork{index, hash, length}
		}
	}

	// Start workers
	for _, peer := range t.Peers {
		go t.startDownloadWorker(peer, workQueue, results, program, id)
	}

	// Collect results into a buffer until full
	//donePieces := len(t.PiecesDone)
	for len(t.PiecesDone) < len(t.PieceHashes) {
		res := <-results
		begin, _ := t.calculateBoundsForPiece(res.index)
		t.saveFile(res.buf, begin)
		//donePieces++
		t.PiecesDone = append(t.PiecesDone, res.index)
		//err := t.saveCache()
		//if err != nil {
		//	program.Send(util.ErrorMsg{TorrentId: id, Err: err.Error()})
		//}
		program.Send(util.ProgressMsg{TorrentId: id, Progress: getCompletePercentage(len(t.PiecesDone), len(t.PieceHashes))})
	}
	close(workQueue)
	return nil
}

func getCompletePercentage(done int, total int) float64 {
	return float64(done) / float64(total)
}

func (t *Session) getCache(buf []byte) error {
	for index, hash := range t.PieceHashes {
		begin, end := t.calculateBoundsForPiece(index)
		if len(buf) >= end {
			bufHash := sha1.Sum(buf[begin:end])
			if !bytes.Equal(bufHash[:], hash[:]) {
				continue
			}
			t.PiecesDone = append(t.PiecesDone, index)
		}
	}
	return nil
}

func (t *Session) initFile() error {
	var buf []byte
	var err error
	if len(t.Files) > 0 {
		buf, err = t.initMultiFile()
		fmt.Println("Init Multi File")
		if err != nil {
			return err
		}
	} else {
		buf, err = t.initSingleFile()
		fmt.Println("Init Single File")
		if err != nil {
			return err
		}
	}
	if buf != nil {
		err = t.getCache(buf)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *Session) initSingleFile() ([]byte, error) {
	if !util.Exists(t.Path) {
		util.MakeDir(t.Path)
	}
	fullPath := filepath.Join(t.Path, t.Name)

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

func (t *Session) initMultiFile() ([]byte, error) {
	basePath := filepath.Join(t.Path, t.Name)
	if !util.Exists(basePath) {
		util.MakeDir(basePath)
		var torrentFile TorrentFile
		for _, torrentFile = range t.Files {
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
	for _, torrentFile = range t.Files {
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

func (t *Session) saveFile(buf []byte, begin int) error {
	if len(t.Files) > 0 {
		err := t.saveMultiFile(buf, begin)
		if err != nil {
			return err
		}
	} else {
		err := t.saveSingleFile(buf, begin)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *Session) saveSingleFile(buf []byte, begin int) error {
	if !util.Exists(t.Path) {
		util.MakeDir(t.Path)
	}
	fullPath := filepath.Join(t.Path, t.Name)
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

func (t *Session) saveMultiFile(buf []byte, begin int) error {
	bytesWritten := 0
	for len(buf) > 0 {
		startPos := begin + bytesWritten
		bytesToWrite := len(buf)
		lengthCounter := 0
		var torrentFile TorrentFile
		for _, torrentFile = range t.Files {
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
		dir := filepath.Join(t.Path, t.Name, d)
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
