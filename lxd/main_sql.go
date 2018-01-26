package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	lxd "github.com/lxc/lxd/client"
)

func cmdSQL(args *Args) error {
	if len(args.Params) != 1 {
		return fmt.Errorf("Invalid arguments")
	}
	query := args.Params[0]

	// Connect to LXD
	c, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	data := internalSQLPost{
		Query: query,
	}
	response, _, err := c.RawQuery("POST", "/internal/sql", data, "")
	if err != nil {
		return err
	}

	result := internalSQLResult{}
	err = json.Unmarshal(response.Metadata, &result)
	if err != nil {
		return err
	}
	if strings.HasPrefix(strings.ToUpper(query), "SELECT") {
		// Print results in tabular format
		widths := make([]int, len(result.Columns))
		for i, column := range result.Columns {
			widths[i] = len(column)
		}
		for _, row := range result.Rows {
			for i, v := range row {
				width := 10
				switch v := v.(type) {
				case string:
					width = len(v)
				case int:
					width = 6
				case int64:
					width = 6
				case time.Time:
					width = 12
				}
				if width > widths[i] {
					widths[i] = width
				}
			}
		}
		format := "|"
		separator := "+"
		columns := make([]interface{}, len(result.Columns))
		for i, column := range result.Columns {
			format += " %-" + strconv.Itoa(widths[i]) + "v |"
			columns[i] = column
			separator += strings.Repeat("-", widths[i]+2) + "+"
		}
		format += "\n"
		separator += "\n"
		fmt.Printf(separator)
		fmt.Printf(fmt.Sprintf(format, columns...))
		fmt.Printf(separator)
		for _, row := range result.Rows {
			fmt.Printf(format, row...)
		}
		fmt.Printf(separator)
	} else {
		fmt.Printf("Rows affected: %d\n", result.RowsAffected)
	}
	return nil
}
