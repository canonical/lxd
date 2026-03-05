package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseColumns(t *testing.T) {
	type item struct {
		Name  string
		Value int
	}

	shorthandMap := map[rune]TypedColumn[item]{
		'n': {Name: "NAME", Data: func(i item) string { return i.Name }},
		'v': {Name: "VALUE", Data: func(i item) string { return "" }},
	}

	// Valid single column.
	cols, err := ParseColumns("n", shorthandMap)
	require.NoError(t, err)
	assert.Len(t, cols, 1)
	assert.Equal(t, "NAME", cols[0].Name)

	// Valid multiple columns.
	cols, err = ParseColumns("nv", shorthandMap)
	require.NoError(t, err)
	assert.Len(t, cols, 2)
	assert.Equal(t, "NAME", cols[0].Name)
	assert.Equal(t, "VALUE", cols[1].Name)

	// Valid comma-separated.
	cols, err = ParseColumns("n,v", shorthandMap)
	require.NoError(t, err)
	assert.Len(t, cols, 2)

	// Unknown column.
	_, err = ParseColumns("x", shorthandMap)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Unknown column shorthand char 'x'")

	// Empty entry (leading comma).
	_, err = ParseColumns(",n", shorthandMap)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Empty column entry")

	// Empty entry (trailing comma).
	_, err = ParseColumns("n,", shorthandMap)
	assert.Error(t, err)

	// Empty entry (double comma).
	_, err = ParseColumns("n,,v", shorthandMap)
	assert.Error(t, err)

	// Empty string.
	_, err = ParseColumns("", shorthandMap)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Empty column entry")
}

func TestDefaultColumnString(t *testing.T) {
	type item struct{ Name string }

	specs := []ShorthandColumn[item]{
		{Shorthand: 'n', Name: "NAME", Data: func(i item) string { return i.Name }},
		{Shorthand: 'd', Name: "DESCRIPTION", Data: func(i item) string { return "" }},
	}

	assert.Equal(t, "nd", DefaultColumnString(specs))
}

func TestParseShorthandColumns(t *testing.T) {
	type item struct {
		Name  string
		Value string
	}

	specs := []ShorthandColumn[item]{
		{Shorthand: 'n', Name: "NAME", Data: func(i item) string { return i.Name }},
		{Shorthand: 'v', Name: "VALUE", Data: func(i item) string { return i.Value }},
	}

	// Parse with default string derived from specs.
	defaultCols := DefaultColumnString(specs)
	cols, err := ParseShorthandColumns(defaultCols, specs)
	require.NoError(t, err)
	assert.Len(t, cols, 2)
	assert.Equal(t, "NAME", cols[0].Name)
	assert.Equal(t, "VALUE", cols[1].Name)

	// Parse subset.
	cols, err = ParseShorthandColumns("v", specs)
	require.NoError(t, err)
	assert.Len(t, cols, 1)
	assert.Equal(t, "VALUE", cols[0].Name)

	// Unknown column.
	_, err = ParseShorthandColumns("x", specs)
	assert.Error(t, err)

	// Verify data functions work.
	testItem := item{Name: "Alice", Value: "100"}
	assert.Equal(t, "100", cols[0].Data(testItem))
}

func TestColumnHeaders(t *testing.T) {
	cols := []TypedColumn[string]{
		{Name: "A", Data: func(s string) string { return s }},
		{Name: "B", Data: func(s string) string { return s }},
	}

	headers := ColumnHeaders(cols)
	assert.Equal(t, []string{"A", "B"}, headers)
}

func TestColumnData(t *testing.T) {
	type item struct {
		Name string
		Age  int
	}

	cols := []TypedColumn[item]{
		{Name: "NAME", Data: func(i item) string { return i.Name }},
	}

	items := []item{
		{Name: "Alice", Age: 30},
		{Name: "Bob", Age: 25},
	}

	data := ColumnData(cols, items)
	assert.Equal(t, [][]string{{"Alice"}, {"Bob"}}, data)
}
