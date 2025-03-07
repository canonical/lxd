package logger

import (
	"encoding/json"
	"fmt"
	"runtime"
)

// Pretty will attempt to convert any Go structure into a string suitable for logging.
func Pretty(input any) string {
	pretty, err := json.MarshalIndent(input, "\t", "\t")
	if err != nil {
		return fmt.Sprint(input)
	}

	return "\n\t" + string(pretty)
}

// GetStack will convert the Go stack into a string suitable for logging.
func GetStack() string {
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)

	return "\n\t" + string(buf[:n])
}
