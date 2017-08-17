package util

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/lxc/lxd/shared"
)

func WriteJSON(w http.ResponseWriter, body interface{}, debug bool) error {
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
