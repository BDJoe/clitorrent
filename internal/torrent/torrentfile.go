package torrent

import (
	"crypto/rand"
	"gotorrent/internal/util"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
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
	program.Send(util.StatusMsg{TorrentId: id, Status: "Initializing Torrent"})
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	tf, err := ParseTorrentFile(file)
	if err != nil {
		return nil, err
	}

	var peerID [20]byte
	_, err = rand.Read(peerID[:])
	if err != nil {
		return nil, err
	}
	tracker := TrackerInfo{Announce: tf.Announce, AnnounceList: tf.AnnounceList, InfoHash: tf.InfoHash}
	peers, err := GetPeers(&tracker, peerID)
	if err != nil {
		return nil, err
	}
	session := Session{
		TrackerInfo: tracker,
		Peers:       peers,
		PeerID:      peerID,
		PieceHashes: tf.PieceHashes,
		PieceLength: tf.PieceLength,
		Length:      tf.Length,
		Name:        tf.Name,
		Files:       tf.Files,
		Path:        downloadPath,
		Tui:         program,
		TorrentID:   id,
	}

	err = session.initFile()
	if err != nil {
		return nil, err
	}
	err = session.createCache()
	if err != nil {
		return nil, err
	}
	program.Send(util.StatusMsg{TorrentId: id, Status: "Ready to download"})
	return &session, nil
}

func (s *Session) createCache() error {
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	path := filepath.Join(cache, "cliTorrent")
	if !util.Exists(path) {
		util.MakeDir(path)
	}
	name := strings.Replace(s.Name, " ", "_", -1) + ".torrent"
	f, err := os.Create(filepath.Join(path, name))
	if err != nil {
		return err
	}
	defer f.Close()
	err = createCacheFile(f, s)
	if err != nil {
		return err
	}
	return nil
}

func GetCachedTorrents(program *tea.Program) ([]*Session, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(cache, "cliTorrent")
	if !util.Exists(path) {
		return nil, err
	}
	files, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var torrents []*Session
	for _, file := range files {
		f, err := os.ReadFile(filepath.Join(path, file.Name()))
		if err != nil {
			return nil, err
		}
		session, err := parseCacheFile(f)
		if err != nil {
			continue
		}

		torrents = append(torrents, session)
	}

	return torrents, nil
}

func InitCachedSession(session *Session) {
	session.Tui.Send(util.StatusMsg{TorrentId: session.TorrentID, Status: "Initializing Torrent"})
	peers, err := GetPeers(&session.TrackerInfo, session.PeerID)
	if err != nil {
		session.Tui.Send(util.ErrorMsg{TorrentId: session.TorrentID, Err: err.Error()})
		return
	}
	session.Peers = peers
	err = session.initFile()
	if err != nil {
		return
	}
	session.Tui.Send(util.StatusMsg{TorrentId: session.TorrentID, Status: "Downloading"})
	session.Tui.Send(util.ProgressMsg{TorrentId: session.TorrentID, Progress: getCompletePercentage(len(session.PiecesDone), len(session.PieceHashes))})
}
