//go:build windows

package main

import (
	"errors"
	"io"
	"os"

	"github.com/mattn/go-colorable"
	"golang.org/x/sys/windows"
)

// Retrieves a wrapped io.WriteCloser object representing the standard output with Windows color support.
func getStdout() io.WriteCloser {
	return &WrappedWriteCloser{os.Stdout, colorable.NewColorableStdout()}
}

// Obtains the file descriptor for the standard output on Windows.
func getStdoutFd() int {
	return int(windows.Stdout)
}

// Fetches the file descriptor for the standard input on Windows.
func getStdinFd() int {
	return int(windows.Stdin)
}

// Retrieves the environment variables specific to Windows as a slice of strings.
func getEnviron() []string {
	return windows.Environ()
}

// Throws an error indicating that the exec operation is not supported on Windows.
func doExec(argv0 string, argv []string, envv []string) error {
	return errors.New("not supported by windows")
}
