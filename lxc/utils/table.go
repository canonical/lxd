package utils

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"

	"github.com/grant-he/lxd/shared/i18n"
	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"
)

// Table list format
const (
	TableFormatCSV   = "csv"
	TableFormatJSON  = "json"
	TableFormatTable = "table"
	TableFormatYAML  = "yaml"
)

// RenderTable renders tabular data in various formats.
func RenderTable(format string, header []string, data [][]string, raw interface{}) error {
	switch format {
	case TableFormatTable:
		table := tablewriter.NewWriter(os.Stdout)
		table.SetAutoWrapText(false)
		table.SetAlignment(tablewriter.ALIGN_LEFT)
		table.SetRowLine(true)
		table.SetHeader(header)
		table.AppendBulk(data)
		table.Render()
	case TableFormatCSV:
		w := csv.NewWriter(os.Stdout)
		w.WriteAll(data)

		err := w.Error()
		if err != nil {
			return err
		}
	case TableFormatJSON:
		enc := json.NewEncoder(os.Stdout)

		err := enc.Encode(raw)
		if err != nil {
			return err
		}
	case TableFormatYAML:
		out, err := yaml.Marshal(raw)
		if err != nil {
			return err
		}

		fmt.Printf("%s", out)
	default:
		return fmt.Errorf(i18n.G("Invalid format %q"), format)
	}

	return nil
}
