package printers

import (
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/olekukonko/tablewriter"
)

type tablePrinter struct {
	options PrintOptions
}

func NewTablePrinter(options PrintOptions) ResourcePrinter {
	return &tablePrinter{
		options: options,
	}
}

func (p *tablePrinter) PrintObj(obj any, writer io.Writer) error {

	rows, err := mustConvertToSliceOfSlices(obj)
	if err != nil {
		return err
	}
	table := getBaseTable(writer, p.options.ColumnLabels, rows)

	if p.options.CompactMode {
		table.SetColumnSeparator("")
		table.SetHeaderLine(false)
		table.SetBorder(false)
	} else {
		table.SetRowLine(true)
	}

	table.Render()

	return nil
}

// TODO integrate more print options here
func getBaseTable(writer io.Writer, header []string, data [][]string) *tablewriter.Table {
	table := tablewriter.NewWriter(writer)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetHeader(header)
	table.AppendBulk(data)
	return table
}

// TODO at some point there should be a more generic object model, which will make this obsolete
func mustConvertToSliceOfSlices(input interface{}) ([][]string, error) {
	inputValue := reflect.ValueOf(input)
	if inputValue.Kind() != reflect.Slice {
		return nil, errors.New("input is not a slice")
	}

	result := make([][]string, inputValue.Len())
	for i := 0; i < inputValue.Len(); i++ {
		inner := inputValue.Index(i)
		if inner.Kind() != reflect.Slice {
			return nil, fmt.Errorf("element at index %d is not a slice", i)
		}

		result[i] = make([]string, inner.Len())
		for j := 0; j < inner.Len(); j++ {
			elem := inner.Index(j)
			if elem.Kind() != reflect.String {
				return nil, fmt.Errorf("element at index (%d, %d) is not a string", i, j)
			}
			result[i][j] = elem.String()
		}
	}

	return result, nil
}
