package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type proxyProcInfo struct {
	listenPid		string
	connectPid		string
	connectAddr		string
	listenAddr		string
}

func createProxyDevInfoFile(devicesPath string, proxyDev string, proxyPid int) error {
	devFileName := fmt.Sprintf("proxy.%s", proxyDev)
	filePath := filepath.Join(devicesPath, devFileName)
	f, err := os.Create(filePath)

	if err != nil {
		return err 
	}

	defer f.Close()

	info := fmt.Sprintf("%d", proxyPid)
	_, err = f.WriteString(info)

	return err
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
		return nil, fmt.Errorf("Proxy device currently doesnt support the connection type: %s", connectionType)
	}
	if listenerType != "tcp" {
		return nil, fmt.Errorf("Proxy device currently doesnt support the listener type: %s", listenerType)
	}

	listenPid := "-1"
	connectPid := "-1"

	if (device["bind"] == "container") {
		listenPid = containerPid
		connectPid = lxdPid
	} else if (device["bind"] == "host") {
		listenPid = lxdPid
		connectPid = containerPid
	} else {
		return nil, fmt.Errorf("No indicated binding side")
	}

	p := &proxyProcInfo{
		listenPid:		listenPid,
		connectPid:		connectPid,
		connectAddr:	connectAddr,
		listenAddr:		listenAddr,
	}

	return p, nil
}

func killProxyProc(devPath string) error {
	contents, err := ioutil.ReadFile(devPath)
	if err != nil {
		return err
	}

	pid, _ := strconv.Atoi(string(contents))
	if err != nil {
		return err
	}
	
	syscall.Kill(pid, syscall.SIGINT)
	os.Remove(devPath)	
	
	return nil
}
