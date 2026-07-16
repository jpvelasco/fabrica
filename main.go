package main

import (
	"os"

	"github.com/jpvelasco/fabrica/cmd/root"
)

func main() {
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
