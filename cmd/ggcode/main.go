package main

import (
	"fmt"
	"os"

	"github.com/topcheer/ggcode/internal/debug"
)

func main() {
	defer debug.Close()

	cmd := NewRootCmd()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		debug.Close()
		os.Exit(1)
	}
}
