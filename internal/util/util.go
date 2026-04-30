package util

import (
	"errors"
	"os"
)

type TorrentState int

const (
	StateInit             TorrentState = 0
	StateStopped          TorrentState = 1
	StateDownloading      TorrentState = 2
	StateDownloadFinished TorrentState = 3
)

type ProgressMsg struct {
	TorrentId int
	Progress  float64
}

type StateMsg struct {
	TorrentId int
	State     TorrentState
}

type StatusMsg struct {
	TorrentId int
	Status    string
}

type ErrorMsg struct {
	TorrentId int
	Err       string
}

func Exists(dir string) bool {
	_, err := os.Stat(dir)
	if err == nil {
		return true
	}
	return !errors.Is(err, os.ErrNotExist)
}

func MakeDir(dir string) {
	err := os.Mkdir(dir, 0755)
	if err != nil {
		panic(err)
	}
}
