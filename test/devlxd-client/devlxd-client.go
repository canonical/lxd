package main

/*
 * An example of how to use lxd's devLXD client.
 * This is intended to be run from inside an instance.
 */

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	lxdClient "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

func main() {
	err := run(os.Args)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	client, err := devLXDClient()
	if err != nil {
		return err
	}

	defer client.Disconnect()

	if len(args) <= 1 {
		fmt.Println("/dev/lxd ok")
		return nil
	}

	command := args[1]

	switch command {
	case "monitor-stream":
		return devLXDMonitorStream()
	case "monitor-websocket":
		eventListener, err := client.GetEvents()
		if err != nil {
			return err
		}

		defer eventListener.Disconnect()

		_, err = eventListener.AddHandler(nil, func(event api.Event) {
			event.Timestamp = time.Time{}

			err := printPrettyJSON(event)
			if err != nil {
				fmt.Printf("Failed to print event: %v\n", err)
				return
			}
		})
		if err != nil {
			return err
		}

		return eventListener.Wait()
	case "ready-state":
		if len(args) != 3 {
			return fmt.Errorf("Usage: %s ready-state <isReadyBool>", args[0])
		}

		ready, err := strconv.ParseBool(args[2])
		if err != nil {
			return err
		}

		req := api.DevLXDPut{
			State: api.Started.String(),
		}

		if ready {
			req.State = api.Ready.String()
		}

		return client.UpdateState(req)
	case "devices":
		devices, err := client.GetDevices()
		if err != nil {
			return err
		}

		return printPrettyJSON(devices)
	case "image-export":
		if len(args) != 3 {
			return fmt.Errorf("Usage: %s image-export <fingerprint>", args[0])
		}

		fingerprint := args[2]

		// Request image export, but disard the received image content.
		req := lxdClient.ImageFileRequest{
			MetaFile:   discardWriteSeeker{},
			RootfsFile: discardWriteSeeker{},
		}

		_, err := client.GetImageFile(fingerprint, req)
		if err != nil {
			return err
		}

		return nil
	case "cloud-init":
		if len(args) != 3 {
			return fmt.Errorf("Usage: %s cloud-init <user-data|vendor-data|network-config>", args[0])
		}

		var config string
		var err error
		switch args[2] {
		case "user-data":
			config, err = client.GetConfigByKey("cloud-init.user-data")
		case "vendor-data":
			config, err = client.GetConfigByKey("cloud-init.vendor-data")
		case "network-config":
			config, err = client.GetConfigByKey("cloud-init.network-config")
		default:
			return fmt.Errorf("Usage: %s cloud-init <user-data|vendor-data|network-config>", args[0])
		}

		if err != nil {
			return err
		}

		fmt.Println(config)

		return nil
	default:
		key, err := client.GetConfigByKey(os.Args[1])
		if err != nil {
			return err
		}

		fmt.Println(key)
		return nil
	}
}

// devLXDClient connects to the LXD socket and returns a devLXD client.
func devLXDClient() (lxdClient.DevLXDServer, error) {
	args := lxdClient.ConnectionArgs{
		UserAgent: "devlxd-client",
	}

	client, err := lxdClient.ConnectDevLXD("/dev/lxd/sock", &args)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// devLXDMonitorStream connects to the LXD socket and listens for events over http stream.
//
// devLXD client supports event monitoring only over a websocket, therefore we use manual
// approach to test the event stream.
func devLXDMonitorStream() error {
	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", "/dev/lxd/sock")
			},
		},
	}

	resp, err := client.Get("http://unix/1.0/events")
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(resp.Body)

	for scanner.Scan() {
		var event api.Event
		err = json.Unmarshal(scanner.Bytes(), &event)
		if err != nil {
			return err
		}

		event.Timestamp = time.Time{}

		err := printPrettyJSON(event)
		if err != nil {
			return err
		}
	}

	return nil
}

// printPrettyJSON prints the given value as JSON to stdout.
func printPrettyJSON(value any) error {
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(out))
	return nil
}

// discardWriteSeeker is a no-op io.WriteSeeker implementation.
type discardWriteSeeker struct{}

// Write discards the input data and returns its length with a nil error.
func (d discardWriteSeeker) Write(p []byte) (int, error) {
	return len(p), nil
}

// Seek does nothing and always returns 0 with a nil error.
func (d discardWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	return 0, nil
}
