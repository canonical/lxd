package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
)

type snapChanges struct {
	Type       string              `json:"type"`
	StatusCode int                 `json:"status-code"`
	Result     []snapChangesResult `json:"result"`
}

type snapChangesResult struct {
	Kind string `json:"kind"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <socket> <snap>\n", os.Args[0])
		os.Exit(1)
	}

	change, err := getLastSnapChange(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s\n", change)
}

func unixHTTPClient(path string) (*http.Client, error) {
	// Setup a Unix socket dialer
	unixDial := func(network, addr string) (net.Conn, error) {
		raddr, err := net.ResolveUnixAddr("unix", path)
		if err != nil {
			return nil, err
		}

		return net.DialUnix("unix", nil, raddr)
	}

	// Define the http transport
	transport := &http.Transport{
		Dial:              unixDial,
		DisableKeepAlives: true,
	}

	// Define the http client
	client := &http.Client{}
	client.Transport = transport

	return client, nil
}

func getLastSnapChange(path string, snap string) (string, error) {
	// Connect to snapd
	client, err := unixHTTPClient(path)
	if err != nil {
		return "", err
	}

	// Prepare the request
	req, err := http.NewRequest("GET", fmt.Sprintf("http://unix.socket/v2/changes?select=in-progress&for=%s", snap), nil)
	if err != nil {
		return "", err
	}

	// Get the changes
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Parse the output
	changes := snapChanges{}
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&changes)
	if err != nil {
		return "", err
	}

	// Check for errors
	if changes.StatusCode != 200 {
		return "", fmt.Errorf("Failed to retrieve changes, status=%d", changes.StatusCode)
	}

	// Return entry
	for _, change := range changes.Result {
		return change.Kind, nil
	}

	return "", nil
}
