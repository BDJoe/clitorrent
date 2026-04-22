package main

import (
	"fmt"
	"gotorrent/internal/tui"
	"os"
)

func main() {
	_, err := tui.Run()
	if err != nil {
		fmt.Printf("An error occurred: %v", err)
		os.Exit(1)
	}
}
