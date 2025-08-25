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
	case "get-state":
		state, err := client.GetState()
		if err != nil {
			return err
		}

		return printPrettyJSON(state)
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
	case "storage":
		usageErr := fmt.Errorf("Usage: %s storage <get|volumes|get-volume|create-volume|update-volume|delete-volume>", args[0])

		if len(args) < 3 {
			return usageErr
		}

		subcmd := args[2]
		switch subcmd {
		case "get":
			if len(args) != 4 {
				return fmt.Errorf("Usage: %s storage get <poolName>", args[0])
			}

			poolName := args[3]

			storage, _, err := client.GetStoragePool(poolName)
			if err != nil {
				return err
			}

			return printPrettyJSON(storage)
		case "volumes":
			if len(args) != 4 {
				return fmt.Errorf("Usage: %s storage volumes <poolName>", args[0])
			}

			poolName := args[3]

			vols, err := client.GetStoragePoolVolumes(poolName)
			if err != nil {
				return err
			}

			return printPrettyJSON(vols)
		case "get-volume":
			fallthrough
		case "get-volume-etag":
			if len(args) != 6 {
				return fmt.Errorf("Usage: %s storage get-volume[-etag] <poolName> <volType> <volName>", args[0])
			}

			poolName := args[3]
			volType := args[4]
			volName := args[5]

			vol, etag, err := client.GetStoragePoolVolume(poolName, volType, volName)
			if err != nil {
				return err
			}

			if subcmd == "get-volume" {
				return printPrettyJSON(vol)
			}

			fmt.Print(etag)
			return nil
		case "create-volume":
			if len(args) != 5 {
				return fmt.Errorf("Usage: %s storage create-volume <poolName> <vol>", args[0])
			}

			poolName := args[3]
			volData := args[4]

			vol := api.DevLXDStorageVolumesPost{}
			err := json.Unmarshal([]byte(volData), &vol)
			if err != nil {
				return err
			}

			return client.CreateStoragePoolVolume(poolName, vol)
		case "update-volume":
			if len(args) < 7 || len(args) > 8 {
				return fmt.Errorf("Usage: %s storage update-volume <poolName> <volType> <volName> <vol> [<etag>]", args[0])
			}

			poolName := args[3]
			volType := args[4]
			volName := args[5]
			volData := args[6]

			etag := ""
			if len(args) == 8 {
				etag = args[7]
			}

			vol := api.DevLXDStorageVolumePut{}
			err := json.Unmarshal([]byte(volData), &vol)
			if err != nil {
				return err
			}

			return client.UpdateStoragePoolVolume(poolName, volType, volName, vol, etag)
		case "delete-volume":
			if len(args) != 6 {
				return fmt.Errorf("Usage: %s storage delete-volume <poolName> <volType> <volName>", args[0])
			}

			poolName := args[3]
			volType := args[4]
			volName := args[5]

			return client.DeleteStoragePoolVolume(poolName, volType, volName)
		default:
			return fmt.Errorf("Unknown subcommand: %q\n%w", subcmd, usageErr)
		}

	case "instance":
		usageErr := fmt.Errorf("Usage: %s instance <get|update>", args[0])

		if len(args) < 3 {
			return usageErr
		}

		subcmd := args[2]
		switch subcmd {
		case "get":
			fallthrough
		case "get-etag":
			if len(args) != 4 {
				return fmt.Errorf("Usage: %s instance get[-etag] <instName>", args[0])
			}

			instName := args[3]

			inst, etag, err := client.GetInstance(instName)
			if err != nil {
				return err
			}

			if subcmd == "get" {
				return printPrettyJSON(inst)
			}

			fmt.Print(etag)
			return nil
		case "update":
			if len(args) < 5 || len(args) > 6 {
				return fmt.Errorf("Usage: %s instance update <instName> <inst> [<etag>]", args[0])
			}

			instName := args[3]
			instData := args[4]

			etag := ""
			if len(args) == 6 {
				etag = args[5]
			}

			var inst api.DevLXDInstancePut
			err := json.Unmarshal([]byte(instData), &inst)
			if err != nil {
				return err
			}

			return client.UpdateInstance(instName, inst, etag)
		default:
			return fmt.Errorf("Unknown subcommand: %q\n%w", subcmd, usageErr)
		}

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
	bearerToken := os.Getenv("DEVLXD_BEARER_TOKEN")
	args := lxdClient.ConnectionArgs{
		UserAgent:   "devlxd-client",
		BearerToken: bearerToken,
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
