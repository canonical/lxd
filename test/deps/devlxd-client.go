package main

/*
 * An example of how to use lxd's golang /dev/lxd client. This is intended to
 * be run from inside a container.
 */

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v2"
)

type devLxdDialer struct {
	Path string
}

func (d devLxdDialer) devLxdDial(network, path string) (net.Conn, error) {
	addr, err := net.ResolveUnixAddr("unix", d.Path)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return nil, err
	}

	return conn, err
}

var devLxdTransport = &http.Transport{
	Dial: devLxdDialer{"/dev/lxd/sock"}.devLxdDial,
}

func devlxdMonitor(c http.Client) {
	dialer := websocket.Dialer{
		NetDial: devLxdTransport.Dial,
	}

	conn, _, err := dialer.Dial("ws://unix.socket/1.0/events", nil)
	if err != nil {
		return
	}

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		message := make(map[string]interface{})
		err = json.Unmarshal(data, &message)
		if err != nil {
			return
		}
		message["timestamp"] = nil

		msg, err := yaml.Marshal(&message)
		if err != nil {
			return
		}

		fmt.Printf("%s\n", msg)
	}
}

func main() {
	c := http.Client{Transport: devLxdTransport}
	raw, err := c.Get("http://meshuggah-rocks/")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if raw.StatusCode != http.StatusOK {
		fmt.Println("http error", raw.StatusCode)
		result, err := ioutil.ReadAll(raw.Body)
		if err != nil {
			os.Exit(1)
		}

		fmt.Println(string(result))
	}

	result := []string{}
	if err := json.NewDecoder(raw.Body).Decode(&result); err != nil {
		fmt.Println("err decoding response", err)
		os.Exit(1)
	}

	if result[0] != "/1.0" {
		fmt.Println("unknown response", result)
		os.Exit(1)
	}

	if len(os.Args) > 1 {
		if os.Args[1] == "monitor" {
			devlxdMonitor(c)
			os.Exit(0)
		}

		raw, err := c.Get(fmt.Sprintf("http://meshuggah-rocks/1.0/config/%s", os.Args[1]))
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		value, err := ioutil.ReadAll(raw.Body)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		fmt.Println(string(value))
	} else {
		fmt.Println("/dev/lxd ok")
	}
}
