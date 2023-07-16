//go:build (linux && !appengine) || darwin || freebsd || openbsd

package main

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Returns the standard output stream as an io.WriteCloser.
func getStdout() io.WriteCloser {
	return os.Stdout
}

// Returns the file descriptor for the standard output.
func getStdoutFd() int {
	return unix.Stdout
}

// Returns the file descriptor for the standard input.
func getStdinFd() int {
	return unix.Stdin
}

// Returns the environment variables as a slice of strings.
func getEnviron() []string {
	return unix.Environ()
}

// Executes a new process with the given command-line arguments and environment variables.
func doExec(argv0 string, argv []string, envv []string) error {
	return unix.Exec(argv0, argv, envv)
}
