package main

import (
	"os"

	"github.com/ashfaqc137/ownci/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
