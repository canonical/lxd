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

// ShorthandColumn pairs a shorthand rune with a column definition.
// Ordered slices of ShorthandColumn are the single source of truth for both
// the default column string and the shorthand lookup map.
type ShorthandColumn[T any] struct {
	Shorthand rune
	Name      string
	Data      func(T) string
}

// DefaultColumnString returns the default column string from an ordered slice
// of column definitions, by concatenating each shorthand rune in order.
func DefaultColumnString[T any](columns []ShorthandColumn[T]) string {
	var sb strings.Builder
	sb.Grow(len(columns))
	for _, col := range columns {
		sb.WriteRune(col.Shorthand)
	}

	return sb.String()
}

// ParseShorthandColumns builds a shorthand map from the given column definitions
// and parses the flagColumns string against it.
func ParseShorthandColumns[T any](flagColumns string, columns []ShorthandColumn[T]) ([]TypedColumn[T], error) {
	shorthandMap := make(map[rune]TypedColumn[T], len(columns))
	for _, col := range columns {
		shorthandMap[col.Shorthand] = TypedColumn[T]{Name: col.Name, Data: col.Data}
	}

	return ParseColumns(flagColumns, shorthandMap)
}

// ParseColumns parses a comma-separated column specification string against a
// shorthand map, returning the selected columns. Each character in the string
// maps to one column via the shorthand map.
func ParseColumns[T any](flagColumns string, shorthandMap map[rune]TypedColumn[T]) ([]TypedColumn[T], error) {
	if flagColumns == "" {
		return nil, fmt.Errorf("Empty column entry (redundant, leading or trailing comma) in %q", flagColumns)
	}

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
