/*
 * An example of how to use lxd's golang /dev/lxd client. This is intended to
 * be run from inside a container.
 */
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
)

type DevLxdDialer struct {
	Path string
}

func (d DevLxdDialer) DevLxdDial(network, path string) (net.Conn, error) {
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

var DevLxdTransport = &http.Transport{
	Dial: DevLxdDialer{"/dev/lxd/sock"}.DevLxdDial,
}

func main() {
	c := http.Client{Transport: DevLxdTransport}
	raw, err := c.Get("http://meshuggah-rocks/")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if raw.StatusCode != http.StatusOK {
		fmt.Println("http error", raw.StatusCode)
		result, err := ioutil.ReadAll(raw.Body)
		if err == nil {
			fmt.Println(string(result))
		}
		os.Exit(1)
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
