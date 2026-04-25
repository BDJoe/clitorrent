package torrent

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"os"

	"github.com/jackpal/bencode-go"
)

type bencodeCache struct {
	DataPath   string `bencode:"data-path"`
	PiecesDone []int  `bencode:"pieces-done"`
}

type bencodeExtensionHandshake struct {
	M            map[string]interface{} `bencode:"m"`
	MetadataSize int                    `bencode:"metadata_size"`
}

type bencodeExtensionMessage struct {
	MsgType   int `bencode:"msg_type"`
	Piece     int `bencode:"piece"`
	TotalSize int `bencode:"total_size"`
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

func serializeExtensionHandshake() []byte {
	m := bencodeExtensionHandshake{M: map[string]interface{}{"ut_metadata": 3}}
	b := new(bytes.Buffer)
	err := bencode.Marshal(b, m)
	if err != nil {
		return nil
	}
	length := len(b.Bytes()) + 2
	buf := make([]byte, 0, length+4)
	buf = binary.BigEndian.AppendUint32(buf, uint32(length))
	buf = append(buf, 20)
	buf = append(buf, 0)
	buf = append(buf, b.Bytes()...)
	return buf
}

func serializeExtensionMessage(m MetadataExtensionMessage) []byte {
	msg := bencodeExtensionMessage{MsgType: m.MsgType, Piece: m.Piece}
	if m.MsgType == 1 {
		msg.TotalSize = m.TotalSize
	}
	msgId := m.ExtensionMessageID.MsgId
	b := new(bytes.Buffer)
	err := bencode.Marshal(b, m)
	if err != nil {
		fmt.Printf("Error serializing extension message: %v\n", err)
		return nil
	}
	length := len(b.Bytes()) + 2
	buf := make([]byte, 0, length+4)
	buf = binary.BigEndian.AppendUint32(buf, uint32(length))
	buf = append(buf, 20)
	buf = append(buf, byte(msgId))
	buf = append(buf, b.Bytes()...)
	fmt.Println(string(buf))
	return buf
}

func parseExtensionHandshake(data []byte) (*bencodeExtensionHandshake, error) {
	message := bencodeExtensionHandshake{}
	buf := bytes.NewReader(data)
	err := bencode.Unmarshal(buf, &message)
	if err != nil {
		return nil, err
	}
	return &message, nil
}

func parseExtensionMessage(data []byte) (*MetadataExtensionMessage, error) {
	message := bencodeExtensionMessage{}
	buf := bytes.NewReader(data)
	err := bencode.Unmarshal(buf, &message)
	if err != nil {
		return nil, err
	}

	ext := MetadataExtensionMessage{MsgType: message.MsgType, Piece: message.Piece, TotalSize: message.TotalSize}
	length := serializeExtensionMessage(ext)
	ext.MetadataChunk = data[len(length):]
	return &ext, nil
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

func ParseTorrentMagnet(b []byte) (TorrentInfo, error) {
	bto := bencodeTorrentFile{}
	buf := bytes.NewReader(b)
	err := bencode.Unmarshal(buf, &bto)
	if err != nil {
		return TorrentInfo{}, err
	}
	return bto.toTorrentInfo()
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
