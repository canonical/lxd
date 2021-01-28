package main

import "os"

func main() {
	root := newRoot()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
