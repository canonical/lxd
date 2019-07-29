package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared"
)

func killProxyProc(pidPath string) error {
	// Get the contents of the pid file
	contents, err := ioutil.ReadFile(pidPath)
	if err != nil {
		return err
	}
	pidString := strings.TrimSpace(string(contents))

	// Check if the process still exists
	if !shared.PathExists(fmt.Sprintf("/proc/%s", pidString)) {
		os.Remove(pidPath)
		return nil
	}

	// Check if it's forkdns
	cmdArgs, err := ioutil.ReadFile(fmt.Sprintf("/proc/%s/cmdline", pidString))
	if err != nil {
		os.Remove(pidPath)
		return nil
	}

	cmdFields := strings.Split(string(bytes.TrimRight(cmdArgs, string("\x00"))), string(byte(0)))
	if len(cmdFields) < 5 || cmdFields[1] != "forkproxy" {
		os.Remove(pidPath)
		return nil
	}

	// Parse the pid
	pidInt, err := strconv.Atoi(pidString)
	if err != nil {
		return err
	}

	// Actually kill the process
	err = unix.Kill(pidInt, unix.SIGKILL)
	if err != nil {
		return err
	}

	// Cleanup
	os.Remove(pidPath)
	return nil
}
