package torrent

import (
	"crypto/rand"
	"gotorrent/internal/util"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/jackpal/bencode-go"
)

type TorrentInfo struct {
	Announce     string
	AnnounceList [][]string
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

// OpenTorrent parses a torrent file
func OpenTorrent(filePath string, downloadPath string, program *tea.Program, id int) (*Session, error) {
	file, err := os.Open(filePath)
	var session Session
	if err != nil {
		return &session, err
	}
	defer file.Close()

	tf, err := ParseTorrentFile(file)
	if err != nil {
		return &session, nil
	}
	err = createCache(filePath, downloadPath)
	if err != nil {
		return &session, err
	}
	session, err = createSession(&tf, downloadPath)
	if err != nil {
		return &session, err
	}
	err = session.initFile()
	if err != nil {
		return &session, err
	}
	session.Tui = program
	session.TorrentID = id
	return &session, nil
}

func createCache(filePath string, downloadPath string) error {
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	path := filepath.Join(cache, "cliTorrent")
	if !util.Exists(path) {
		util.MakeDir(path)
	}
	_, name := filepath.Split(filePath)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	f, err := os.Create(filepath.Join(path, name+".temp"))
	if err != nil {
		return err
	}
	defer f.Close()
	c := bencodeCache{DataPath: downloadPath}
	err = bencode.Marshal(f, c)
	if err != nil {
		return err
	}
	return nil
}

//func GetCachedTorrents() ([]*Session, error) {
//	cache, err := os.UserCacheDir()
//	if err != nil {
//		return nil, err
//	}
//	path := filepath.Join(cache, "cliTorrent")
//	if !util.Exists(path) {
//		return nil, err
//	}
//	files, err := os.ReadDir(path)
//	if err != nil {
//		return nil, err
//	}
//	var torrents []*Session
//	for _, file := range files {
//		if !strings.Contains(file.Name(), ".temp") {
//			continue
//		}
//		name := strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
//		dataPath, _, err := getCacheFile(filepath.Join(path, file.Name()))
//		session, err := OpenTorrent(filepath.Join(path, name+".torrent"), dataPath)
//		if err != nil {
//			continue
//		}
//
//		torrents = append(torrents, session)
//	}
//
//	return torrents, nil
//}

func createSession(t *TorrentInfo, downloadPath string) (Session, error) {
	var peerID [20]byte
	_, err := rand.Read(peerID[:])
	if err != nil {
		return Session{}, err
	}
	tracker := TrackerInfo{Announce: t.Announce, AnnounceList: t.AnnounceList, InfoHash: t.InfoHash}
	torrent := Session{
		TrackerInfo: tracker,
		PeerID:      peerID,
		PieceHashes: t.PieceHashes,
		PieceLength: t.PieceLength,
		Length:      t.Length,
		Name:        t.Name,
		Files:       t.Files,
		Path:        downloadPath,
	}
	return torrent, nil
}
