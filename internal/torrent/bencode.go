package torrent

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"os"

	"github.com/jackpal/bencode-go"
)

type bencodeCache struct {
	DataPath   string `bencode:"data-path"`
	PiecesDone []int  `bencode:"pieces-done"`
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

type bencodeTorrentFile struct {
	Announce     string          `bencode:"announce"`
	AnnounceList [][]string      `bencode:"announce-list"`
	Info         bencodeInfoBase `bencode:"info"`
}

func getCacheFile(path string) (string, []int, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	bc := bencodeCache{}
	err = bencode.Unmarshal(file, &bc)
	if err != nil {
		return "", nil, err
	}

	return bc.DataPath, bc.PiecesDone, nil
}

func ParseTorrentFile(file *os.File) (TorrentInfo, error) {
	bto := bencodeTorrentFile{}
	err := bencode.Unmarshal(file, &bto)
	if err != nil {
		return TorrentInfo{}, err
	}
	return bto.toTorrentInfo()
}

func (bto *bencodeTorrentFile) toTorrentInfo() (TorrentInfo, error) {
	if bto.Info.Length == 0 {
		t, err := parseMultiTorrent(bto)
		if err != nil {
			return TorrentInfo{}, err
		}

		return t, nil
	}
	t, err := parseSingleTorrent(bto)
	if err != nil {
		return TorrentInfo{}, err
	}

	return t, nil
}

func parseMultiTorrent(bt *bencodeTorrentFile) (TorrentInfo, error) {
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
	pieceHashes, err := splitPieceHashes(bt.Info.Pieces)
	if err != nil {
		return TorrentInfo{}, err
	}

	var files []TorrentFile
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

func parseSingleTorrent(bt *bencodeTorrentFile) (TorrentInfo, error) {
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
	pieceHashes, err := splitPieceHashes(bt.Info.Pieces)
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

func splitPieceHashes(pieces string) ([][20]byte, error) {
	hashLen := 20 // Length of SHA-1 hash
	buf := []byte(pieces)
	if len(buf)%hashLen != 0 {
		err := fmt.Errorf("received malformed pieces of length %d", len(buf))
		return nil, err
	}
	numHashes := len(buf) / hashLen
	hashes := make([][20]byte, numHashes)

	for i := range numHashes {
		copy(hashes[i][:], buf[i*hashLen:(i+1)*hashLen])
	}
	return hashes, nil
}
