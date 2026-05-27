package torrent

import (
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
	var session *Session
	if strings.HasPrefix(filePath, "magnet:") {
		tf, err := ParseMagnetLink(filePath)
		if err != nil {
			return nil, err
		}
		session, err = newSessionFromMagnet(tf)
		if err != nil {
			return nil, err
		}
	} else {
		file, err := os.Open(filePath)
		if err != nil {
			return nil, err
		}
		defer file.Close()

		tf, err := ParseTorrentFile(file)
		if err != nil {
			return nil, err
		}
		session, err = newSessionFromFile(tf)
		if err != nil {
			return nil, err
		}
	}

	session.Tui = program
	session.TorrentID = id
	session.Path = downloadPath

	if !session.isMagnet {
		err := session.initFile()
		if err != nil {
			return nil, err
		}
		err = session.createCache()
		if err != nil {
			return nil, err
		}
	}

	program.Send(util.StatusMsg{TorrentId: id, Status: "Ready to download"})
	return session, nil
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

func GetCachedTorrents() ([]*Session, error) {
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
	err := session.initFile()
	if err != nil {
		return
	}
	session.Tui.Send(util.StatusMsg{TorrentId: session.TorrentID, Status: "Ready"})
	session.Tui.Send(util.ProgressMsg{TorrentId: session.TorrentID, Progress: getCompletePercentage(len(session.PiecesDone), len(session.PieceHashes))})
}
