package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/lxc/lxd/shared"
)

func WriteJSON(w http.ResponseWriter, body interface{}) error {
	var output io.Writer
	var captured *bytes.Buffer

	output = w
	if debug {
		captured = &bytes.Buffer{}
		output = io.MultiWriter(w, captured)
	}

	err := json.NewEncoder(output).Encode(body)

	if captured != nil {
		shared.DebugJson(captured)
	}

	return err
}

func loadModule(module string) error {
	if shared.PathExists(fmt.Sprintf("/sys/module/%s", module)) {
		return nil
	}

	return shared.RunCommand("modprobe", module)
}
