/*
 * An example of how to use lxd's golang /dev/lxd client. This is intended to
 * be run from inside a container.
 */
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/lxc/lxd"
)

func main() {
	c := http.Client{Transport: lxd.DevLxdTransport}
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

	fmt.Println("/dev/lxd ok")
}
