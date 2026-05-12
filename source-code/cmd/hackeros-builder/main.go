package main

import (
	"hackeros-builder/src/cli"
	"os"
)

var Version = "0.1.0"

func main() {
	app := cli.NewApp(Version)
	if err := app.Run(os.Args); err != nil {
		os.Exit(1)
	}
}
