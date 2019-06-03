package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxc/lxd/shared"
)

type proxyProcInfo struct {
	listenPid   string
	connectPid  string
	connectAddr string
	listenAddr  string
}

func setupProxyProcInfo(c container, device map[string]string) (*proxyProcInfo, error) {
	pid := c.InitPID()
	containerPid := strconv.Itoa(int(pid))
	lxdPid := strconv.Itoa(os.Getpid())

	connectAddr := device["connect"]
	listenAddr := device["listen"]

	connectionType := strings.SplitN(connectAddr, ":", 2)[0]
	listenerType := strings.SplitN(listenAddr, ":", 2)[0]

	if connectionType != "tcp" {
		return nil, fmt.Errorf("Proxy device doesn't support the connection type: %s", connectionType)
	}
	if listenerType != "tcp" {
		return nil, fmt.Errorf("Proxy device doesn't support the listener type: %s", listenerType)
	}

	var listenPid string
	var connectPid string

	bindVal, exists := device["bind"]

	if bindVal == "host" || !exists {
		listenPid = lxdPid
		connectPid = containerPid
	} else if bindVal == "container" {
		listenPid = containerPid
		connectPid = lxdPid
	} else {
		return nil, fmt.Errorf("Invalid binding side given. Must be \"host\" or \"container\"")
	}

	p := &proxyProcInfo{
		listenPid:   listenPid,
		connectPid:  connectPid,
		connectAddr: connectAddr,
		listenAddr:  listenAddr,
	}

	return p, nil
}

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
	err = syscall.Kill(pidInt, syscall.SIGKILL)
	if err != nil {
		return err
	}

	// Cleanup
	os.Remove(pidPath)
	return nil
}
