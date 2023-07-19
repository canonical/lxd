package main

import (
	"os"
)

// main is the entry point of the program,
// it initializes the root command and executes it, handling any errors.
func main() {
	root := newRoot()
	err := root.Execute()
	if err != nil {
		os.Exit(1)
	}
}
