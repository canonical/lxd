package logger

import (
	"encoding/json"
	"fmt"
)

func Pretty(input interface{}) string {
	pretty, err := json.MarshalIndent(input, "\t", "\t")
	if err != nil {
		return fmt.Sprintf("%s", input)
	}

	return fmt.Sprintf("\n\t%s", pretty)
}
