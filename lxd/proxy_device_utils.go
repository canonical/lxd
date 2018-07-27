package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

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

	listenPid := "-1"
	connectPid := "-1"

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

	// Check if it's a proxy process
	cmdPath, err := os.Readlink(fmt.Sprintf("/proc/%s/exe", pidString))
	if err != nil {
		cmdPath = ""
	}

	// Deal with deleted paths
	cmdName := filepath.Base(strings.Split(cmdPath, " ")[0])
	if cmdName != "lxd" {
		os.Remove(pidPath)
		return nil
	}

	// Parse the pid
	pidInt, err := strconv.Atoi(pidString)
	if err != nil {
		return err
	}

	err = syscall.Kill(pidInt, syscall.SIGTERM)
	if err != nil {
		return err
	}

	go func() {
		for i := 0; i < 6; i++ {
			time.Sleep(500 * time.Millisecond)
			// Check if the process still exists
			if !shared.PathExists(fmt.Sprintf("/proc/%s", pidString)) {
				return
			}

			// Check if it's a proxy process
			cmdPath, err := os.Readlink(fmt.Sprintf("/proc/%s/exe", pidString))
			if err != nil {
				cmdPath = ""
			}

			// Deal with deleted paths
			cmdName := filepath.Base(strings.Split(cmdPath, " ")[0])
			if cmdName != "lxd" {
				return
			}
		}
		syscall.Kill(pidInt, syscall.SIGKILL)
	}()

	// Cleanup
	os.Remove(pidPath)
	return nil
}
