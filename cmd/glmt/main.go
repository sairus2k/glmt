package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// TODO: wire up auth, config, gitlab client, and TUI
	fmt.Println("glmt — GitLab Merge Train CLI")
	return nil
}
