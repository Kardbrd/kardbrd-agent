package main

import (
	"os"

	"github.com/Kardbrd/kardbrd-agent/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
