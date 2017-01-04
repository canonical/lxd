package api

import (
	"encoding/json"
)

// ResponseRaw represents a LXD operation in its original form
type ResponseRaw struct {
	Response `yaml:",inline"`

	Metadata interface{} `json:"metadata"`
}

// Response represents a LXD operation
type Response struct {
	Type ResponseType `json:"type"`

	// Valid only for Sync responses
	Status     string `json:"status"`
	StatusCode int    `json:"status_code"`

	// Valid only for Async responses
	Operation string `json:"operation"`

	// Valid only for Error responses
	Code  int    `json:"error_code"`
	Error string `json:"error"`

	// Valid for Sync and Error responses
	Metadata json.RawMessage `json:"metadata"`
}

// MetadataAsMap parses the Response metadata into a map
func (r *Response) MetadataAsMap() (map[string]interface{}, error) {
	ret := map[string]interface{}{}
	err := r.MetadataAsStruct(&ret)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// MetadataAsOperation turns the Response metadata into an Operation
func (r *Response) MetadataAsOperation() (*Operation, error) {
	op := Operation{}
	err := r.MetadataAsStruct(&op)
	if err != nil {
		return nil, err
	}

	return &op, nil
}

// MetadataAsStringSlice parses the Response metadata into a slice of string
func (r *Response) MetadataAsStringSlice() ([]string, error) {
	sl := []string{}
	err := r.MetadataAsStruct(&sl)
	if err != nil {
		return nil, err
	}

	return sl, nil
}

// MetadataAsStruct parses the Response metadata into a provided struct
func (r *Response) MetadataAsStruct(target interface{}) error {
	if err := json.Unmarshal(r.Metadata, &target); err != nil {
		return err
	}

	return nil
}

// ResponseType represents a valid LXD response type
type ResponseType string

// LXD response types
const (
	SyncResponse  ResponseType = "sync"
	AsyncResponse ResponseType = "async"
	ErrorResponse ResponseType = "error"
)
