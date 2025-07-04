package drivers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// createBodyReader creates a reader for the given request body contents.
func createBodyReader(contents map[string]any) (io.Reader, error) {
	body := &bytes.Buffer{}

	err := json.NewEncoder(body).Encode(contents)
	if err != nil {
		return nil, fmt.Errorf("Failed to write request body: %w", err)
	}

	return body, nil
}
