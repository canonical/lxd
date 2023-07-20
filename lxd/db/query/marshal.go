package query

import (
	"fmt"
)

type Marshaler interface {
	MarshalDB() (string, error)
}

type Unmarshaler interface {
	UnmarshalDB(string) error
}

// Marshal transforms a given value into a string format if it implements the DBMarshaler interface.
func Marshal(v any) (string, error) {
	marshaller, ok := v.(Marshaler)
	if !ok {
		return "", fmt.Errorf("Cannot marshal data, type does not implement DBMarshaler")
	}

	return marshaller.MarshalDB()
}

// Unmarshal decodes data from a string format into a provided value if it implements the DBUnmarshaler interface.
func Unmarshal(data string, v any) error {
	if v == nil {
		return fmt.Errorf("Cannot unmarshal data into nil value")
	}

	unmarshaler, ok := v.(Unmarshaler)
	if !ok {
		return fmt.Errorf("Cannot marshal data, type does not implement DBUnmarshaler")
	}

	return unmarshaler.UnmarshalDB(data)
}
