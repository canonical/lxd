package logger

import (
	"encoding/json"
	"fmt"
)

// Pretty will attempt to convert any Go structure into a string suitable for logging
func Pretty(input interface{}) string {
	pretty, err := json.MarshalIndent(input, "\t", "\t")
	if err != nil {
		return fmt.Sprintf("%v", input)
	}

	return fmt.Sprintf("\n\t%s", pretty)
}
