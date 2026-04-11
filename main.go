package main

import (
	"fmt"
	tui "gotorrent/internal/tui"
	"os"
)

func main() {
	// inPath := os.Args[1]
	// outPath := os.Args[2]

	// tf, err := torrentFile.Open(inPath)
	// if err != nil {
	// 	log.Fatal(err)
	// }

	// err = tf.DownloadToFile(outPath)
	// if err != nil {
	// 	log.Fatal(err)
	// }

	// fmt.Println("Download Complete!")

	err := tui.Run()
	if err != nil {
		fmt.Printf("An error occurred: %v", err)
		os.Exit(1)
	}
}
