package torrent

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
)

type Magnet struct {
	InfoHash [20]byte
	Trackers [][]string
	Name     string
}

const MagnetLink = "magnet:?xt=urn:btih:7e0636e1b0a6e32955082b37f3db10d6a953a5a3&dn=Dungeon%20Crawler%20Carl%20-%20Book%201%20-%20Matt%20Dinniman&tr=udp%3A%2F%2Ftracker.coppersurfer.tk%3A6969&tr=udp%3A%2F%2Ftracker.leechers-paradise.org%3A6969&tr=udp%3A%2F%2Ftracker.torrent.eu.org%3A451%2Fannounce&tr=udp%3A%2F%2Ftracker.open-internet.nl%3A6969%2Fannounce&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A69691337%2Fannounce&tr=udp%3A%2F%2Ftracker.vanitycore.co%3A6969%2Fannounce&tr=http%3A%2F%2Ftracker.baravik.org%3A6970%2Fannounce&tr=http%3A%2F%2Fretracker.telecom.by%3A80%2Fannounce&tr=http%3A%2F%2Ftracker.vanitycore.co%3A6969%2Fannounce"

func ParseMagnetLink(magnet string) (Magnet, error) {
	var mag Magnet
	link, err := url.Parse(magnet)
	if err != nil {
		return mag, err
	}
	if link.Scheme != "magnet" {
		return mag, errors.New("malformed magnet link")
	}
	params, err := url.ParseQuery(link.RawQuery)
	if err != nil {
		return mag, err
	}
	if data, ok := params["xt"]; ok {
		if len(data) != 1 {
			return mag, errors.New("malformed magnet link")
		}
		hashParts := strings.Split(data[0], ":")
		if hashParts[1] != "btih" {
			return mag, errors.New("v2 magnet not supported")
		}
		hash, err := hex.DecodeString(hashParts[2])
		if err != nil {
			return mag, err
		}
		if len(hash) != 20 {
			return mag, errors.New("malformed infohash")
		}
		mag.InfoHash = [20]byte(hash[:])
	} else {
		return mag, errors.New("malformed magnet link")
	}
	if data, ok := params["dn"]; ok {
		mag.Name = data[0]
	}
	if data, ok := params["tr"]; ok {
		for _, tracker := range data {
			mag.Trackers = append(mag.Trackers, []string{tracker})
		}
	}
	return mag, nil
}

func GetMetadata(link string) (*TorrentInfo, error) {
	mag, err := ParseMagnetLink(link)
	if err != nil {
		return nil, err
	}

	var peerID [20]byte
	_, err = rand.Read(peerID[:])
	if err != nil {
		return nil, err
	}
	track := TrackerInfo{InfoHash: mag.InfoHash, AnnounceList: mag.Trackers}
	peers, err := GetPeers(&track, peerID)
	if err != nil {
		return nil, err
	}
	for _, peer := range peers {
		info := GetMetadataFromPeer(peer, peerID, mag.InfoHash)
		if info != nil {
			return info, nil
		}
	}
	return nil, errors.New("no peers found")
}

func GetMetadataFromPeer(peer Peer, peerId [20]byte, infoHash [20]byte) *TorrentInfo {
	c, err :=
		newMagnetClient(peer, peerId, infoHash)
	if err != nil {
		log.Printf("Could not handshake with %s. %s\n", peer.IP, err)
		return nil
	}
	defer c.Conn.Close()
	for {
		if c.Choked {
			err := c.SendInterested()
			if err != nil {
				log.Printf("%s\n", err)
			}

			err = c.SendUnchoke()
			if err != nil {
				log.Printf("%s\n", err)
			}

			err = c.ReadMessages()
			if err != nil {
				log.Printf("error reading message from magnet:%s\n", err)
				break
			}
			continue
		}
		break
	}

	metadata, err := c.getMetadata()
	if err != nil {
		log.Printf("error getting metadata: %s\n", err)
	}

	metadataHash := sha1.Sum(metadata)

	if !bytes.Equal(metadataHash[:], infoHash[:]) {
		fmt.Printf("The fetched metadata hash doesn't match info hash\n")
		return nil
	}

	info, err := ParseTorrentMagnet(metadata)
	if err != nil {
		log.Printf("error parsing info: %s\n", err)
	}
	return &info
}
