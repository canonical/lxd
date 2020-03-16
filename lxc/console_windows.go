// +build windows

package main

import (
	"fmt"

	"github.com/gorilla/websocket"
)

func (c *cmdConsole) getTERM() (string, bool) {
	return "dumb", true
}

func (c *cmdConsole) controlSocketHandler(control *websocket.Conn) {
	// TODO: figure out what the equivalent of signal.SIGWINCH is on
	// windows and use that; for now if you resize your terminal it just
	// won't work quite correctly.
	err := c.sendTermSize(control)
	if err != nil {
		fmt.Printf("error setting term size %s\n", err)
	}
}
