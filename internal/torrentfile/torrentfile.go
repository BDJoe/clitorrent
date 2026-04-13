package torrentFile

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"fmt"
	"gotorrent/internal/p2p"
	"gotorrent/internal/peers"
	"gotorrent/internal/util"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	bencode "github.com/jackpal/bencode-go"
)

// Port to listen on
const Port uint16 = 6881

// TorrentFile encodes the metadata from a .torrent file
type TorrentInfo struct {
	Announce     string
	AnnounceList [][]string
	Comment      string
	InfoHash     [20]byte
	PieceHashes  [][20]byte
	PieceLength  int
	Length       int
	Name         string
	Files        []TorrentFile
}

type TorrentFile struct {
	Length int
	Path   []string
}

type bencodeInfoBase struct {
	Pieces      string        `bencode:"pieces"`
	PieceLength int           `bencode:"piece length"`
	Length      int           `bencode:"length"`
	Name        string        `bencode:"name"`
	Files       []bencodeFile `bencode:"files"`
}

type bencodeInfoSingle struct {
	Pieces      string `bencode:"pieces"`
	PieceLength int    `bencode:"piece length"`
	Length      int    `bencode:"length"`
	Name        string `bencode:"name"`
}

type bencodeInfoMulti struct {
	Pieces      string        `bencode:"pieces"`
	PieceLength int           `bencode:"piece length"`
	Name        string        `bencode:"name"`
	Files       []bencodeFile `bencode:"files"`
}

type bencodeFile struct {
	Length int      `bencode:"length"`
	Path   []string `bencode:"path"`
}

type bencodeTorrent struct {
	Announce     string          `bencode:"announce"`
	AnnounceList [][]string      `bencode:"announce-list"`
	Info         bencodeInfoBase `bencode:"info"`
}

func (t *TorrentInfo) DownloadToFile(path string, program *tea.Program) error {
	program.Send(util.ProgressMsg{Progress: 0.0, Message: "Connecting to peers"})
	var peerID [20]byte
	_, err := rand.Read(peerID[:])
	if err != nil {
		return err
	}
	peers := []peers.Peer{}
	if len(t.AnnounceList) == 0 {
		peers, err = t.requestPeers(t.Announce, peerID, Port)
		if err != nil {
			return err
		}
	} else {
		for _, announce := range t.AnnounceList {
			// program.Send(util.ProgressMsg{Progress: 0.0, Message: fmt.Sprintf("Connecting to %s\n", announce[0])})
			//t.Message = fmt.Sprintf("Connecting to %s\n", announce[0])
			newPeers, err := t.requestPeers(announce[0], peerID, Port)
			if err != nil {
				//fmt.Println(err)
				continue
			}
			peers = append(peers, newPeers...)
			program.Send(util.ProgressMsg{Progress: 0.0, Message: fmt.Sprintf("Success! Got %d peers.", len(peers))})
			//t.Message = fmt.Sprintf("Success! Got %d peers.", len(newPeers))
		}
	}

	if len(peers) == 0 {
		return fmt.Errorf("Failed to connect to trackers\n")
	}
	program.Send(util.ProgressMsg{Message: fmt.Sprintf("Connected to %d peers", len(peers))})
	// TODO: Handle multifile torrents
	torrent := p2p.Torrent{
		Peers:       peers,
		PeerID:      peerID,
		InfoHash:    t.InfoHash,
		PieceHashes: t.PieceHashes,
		PieceLength: t.PieceLength,
		Length:      t.Length,
		Name:        t.Name,
	}

	buf, err := torrent.Download(program)
	if err != nil {
		return err
	}

	if len(t.Files) > 0 {
		err = t.saveMultiFile(path, buf)
		if err != nil {
			return err
		}
	} else {
		err = t.saveSingleFile(path, buf)
		if err != nil {
			return err
		}
	}
	program.Send(util.ProgressMsg{Message: "Download Complete!"})
	return nil
}

func (t *TorrentInfo) saveMultiFile(path string, buf []byte) error {
	bytesWritten := 0
	for _, file := range t.Files {
		filePath := filepath.Join(file.Path...)
		d, f := filepath.Split(filePath)
		dir := filepath.Join(path, t.Name, d)
		fullPath := filepath.Join(dir, f)

		if !util.DirExists(dir) {
			util.MakeDir(dir)
		}

		outFile, err := os.Create(fullPath)
		if err != nil {
			return err
		}
		defer outFile.Close()
		_, err = outFile.Write(buf[bytesWritten : bytesWritten+file.Length])
		if err != nil {
			return err
		}
		bytesWritten += file.Length
	}
	return nil
}

func (t *TorrentInfo) saveSingleFile(path string, buf []byte) error {
	if !util.DirExists(path) {
		util.MakeDir(path)
	}

	outFile, err := os.Create(path + t.Name)
	if err != nil {
		return err
	}
	defer outFile.Close()
	_, err = outFile.Write(buf)
	if err != nil {
		return err
	}
	return nil
}

// Open parses a torrent file
func Open(path string) (TorrentInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return TorrentInfo{}, err
	}
	defer file.Close()

	bto := bencodeTorrent{}
	err = bencode.Unmarshal(file, &bto)
	if err != nil {
		return TorrentInfo{}, err
	}

	//var data any
	return bto.toTorrentFile()
}

func (i *bencodeInfoSingle) hash() ([20]byte, error) {
	var buf bytes.Buffer
	err := bencode.Marshal(&buf, *i)
	if err != nil {
		return [20]byte{}, err
	}

	h := sha1.Sum(buf.Bytes())

	//fmt.Printf("%x\n", h)
	return h, nil
}

func (i *bencodeInfoMulti) hash() ([20]byte, error) {
	var buf bytes.Buffer
	err := bencode.Marshal(&buf, *i)
	if err != nil {
		return [20]byte{}, err
	}

	h := sha1.Sum(buf.Bytes())

	return h, nil
}

func (i *bencodeInfoBase) splitPieceHashes() ([][20]byte, error) {
	hashLen := 20 // Length of SHA-1 hash
	buf := []byte(i.Pieces)
	if len(buf)%hashLen != 0 {
		err := fmt.Errorf("Received malformed pieces of length %d", len(buf))
		return nil, err
	}
	numHashes := len(buf) / hashLen
	hashes := make([][20]byte, numHashes)

	for i := range numHashes {
		copy(hashes[i][:], buf[i*hashLen:(i+1)*hashLen])
	}
	return hashes, nil
}

func (bto *bencodeTorrent) toTorrentFile() (TorrentInfo, error) {
	if bto.Info.Length == 0 {
		t, err := parseMultiTorrent(bto)
		if err != nil {
			return TorrentInfo{}, err
		}

		return t, nil
	} else {
		t, err := parseSingleTorrent(bto)
		if err != nil {
			return TorrentInfo{}, err
		}

		return t, nil
	}
}

func parseMultiTorrent(bt *bencodeTorrent) (TorrentInfo, error) {
	info := bencodeInfoMulti{
		Pieces:      bt.Info.Pieces,
		PieceLength: bt.Info.PieceLength,
		Name:        bt.Info.Name,
		Files:       bt.Info.Files,
	}
	infoHash, err := info.hash()
	if err != nil {
		return TorrentInfo{}, err
	}
	pieceHashes, err := bt.Info.splitPieceHashes()
	if err != nil {
		return TorrentInfo{}, err
	}

	files := []TorrentFile{}
	length := 0
	for _, file := range info.Files {
		files = append(files, TorrentFile{Length: file.Length, Path: file.Path})
		length += file.Length
	}
	t := TorrentInfo{
		Announce:     bt.Announce,
		AnnounceList: bt.AnnounceList,
		InfoHash:     infoHash,
		PieceHashes:  pieceHashes,
		PieceLength:  bt.Info.PieceLength,
		Files:        files,
		Name:         bt.Info.Name,
		Length:       length,
	}

	return t, nil
}

func parseSingleTorrent(bt *bencodeTorrent) (TorrentInfo, error) {
	info := bencodeInfoSingle{
		Pieces:      bt.Info.Pieces,
		PieceLength: bt.Info.PieceLength,
		Name:        bt.Info.Name,
		Length:      bt.Info.Length,
	}
	infoHash, err := info.hash()
	if err != nil {
		return TorrentInfo{}, err
	}
	pieceHashes, err := bt.Info.splitPieceHashes()
	if err != nil {
		return TorrentInfo{}, err
	}

	t := TorrentInfo{
		Announce:     bt.Announce,
		AnnounceList: bt.AnnounceList,
		InfoHash:     infoHash,
		PieceHashes:  pieceHashes,
		PieceLength:  bt.Info.PieceLength,
		Length:       bt.Info.Length,
		Name:         bt.Info.Name,
	}

	return t, nil
}
