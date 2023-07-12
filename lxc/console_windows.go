//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gorilla/websocket"
)

// getTERM returns a hardcoded terminal type ("dumb") and a boolean indicating the terminal type is set.
func (c *cmdConsole) getTERM() (string, bool) {
	return "dumb", true
}

// controlSocketHandler initializes the console size in the websocket connection, noting resize won't be handled correctly on Windows.
func (c *cmdConsole) controlSocketHandler(control *websocket.Conn) {
	// TODO: figure out what the equivalent of signal.SIGWINCH is on
	// windows and use that; for now if you resize your terminal it just
	// won't work quite correctly.
	err := c.sendTermSize(control)
	if err != nil {
		fmt.Printf("error setting term size %s\n", err)
	}
}

// findCommand looks up the executable path for a given command name, with special handling for "VirtViewer" on Windows.
func (c *cmdConsole) findCommand(name string) string {
	path, _ := exec.LookPath(name)
	if path == "" {
		// Let's see if it's not in the usual location.
		programs, err := os.ReadDir("\\Program Files")
		if err != nil {
			return ""
		}

		for _, entry := range programs {
			if strings.HasPrefix(entry.Name(), "VirtViewer") {
				return filepath.Join("\\Program Files", entry.Name(), "bin", "remote-viewer.exe")
			}
		}
	}

	return path
}
