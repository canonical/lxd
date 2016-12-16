package util

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
)

func WriteJSON(w http.ResponseWriter, body interface{}) error {
	var output io.Writer
	var captured *bytes.Buffer

	output = w
	if state.Debug {
		captured = &bytes.Buffer{}
		output = io.MultiWriter(w, captured)
	}

	err := json.NewEncoder(output).Encode(body)

	if captured != nil {
		shared.DebugJson(captured)
	}

	return err
}

func EtagHash(data interface{}) (string, error) {
	etag := sha256.New()
	err := json.NewEncoder(etag).Encode(data)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", etag.Sum(nil)), nil
}

func EtagCheck(r *http.Request, data interface{}) error {
	match := r.Header.Get("If-Match")
	if match == "" {
		return nil
	}

	hash, err := EtagHash(data)
	if err != nil {
		return err
	}

	if hash != match {
		return fmt.Errorf("ETag doesn't match: %s vs %s", hash, match)
	}

	return nil
}

func LoadModule(module string) error {
	if shared.PathExists(fmt.Sprintf("/sys/module/%s", module)) {
		return nil
	}

	return shared.RunCommand("modprobe", module)
}
