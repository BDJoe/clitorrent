package util

import (
	"errors"
	"os"
)

type ProgressMsg struct {
	TorrentId int
	Progress  float64
}

type StatusMsg struct {
	TorrentId int
	Status    string
}

type ErrorMsg struct {
	TorrentId int
	Err       string
}

func DirExists(dir string) bool {
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
