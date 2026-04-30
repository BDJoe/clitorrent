package util

import (
	"errors"
	"os"
)

type MessageType int

const (
	MsgProgress MessageType = 0
	MsgStatus   MessageType = 1
	MsgError    MessageType = 2
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
