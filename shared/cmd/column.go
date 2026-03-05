package cmd

import (
	"fmt"
	"strings"
)

// TypedColumn defines a table column with a strongly typed data extraction function.
type TypedColumn[T any] struct {
	Name string
	Data func(T) string
}

// ParseColumns parses a comma-separated column specification string against a
// shorthand map, returning the selected columns. Each character in the string
// maps to one column via the shorthand map.
func ParseColumns[T any](flagColumns string, shorthandMap map[rune]TypedColumn[T]) ([]TypedColumn[T], error) {
	columnList := strings.Split(flagColumns, ",")
	columns := make([]TypedColumn[T], 0, len(flagColumns))

	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf("Empty column entry (redundant, leading or trailing comma) in %q", flagColumns)
		}

		for _, columnRune := range columnEntry {
			column, ok := shorthandMap[columnRune]
			if !ok {
				return nil, fmt.Errorf("Unknown column shorthand char '%c' in %q", columnRune, columnEntry)
			}

			columns = append(columns, column)
		}
	}

	return columns, nil
}

// ColumnHeaders returns the header names from a list of typed columns.
func ColumnHeaders[T any](columns []TypedColumn[T]) []string {
	headers := make([]string, len(columns))
	for i, column := range columns {
		headers[i] = column.Name
	}

	return headers
}

// ColumnData generates table data rows from a slice of items using the given columns.
func ColumnData[T any](columns []TypedColumn[T], items []T) [][]string {
	data := make([][]string, len(items))
	for i, item := range items {
		row := make([]string, len(columns))
		for j, column := range columns {
			row[j] = column.Data(item)
		}

		data[i] = row
	}

	return data
}
