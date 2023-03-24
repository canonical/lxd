package printers

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTablePrinter(t *testing.T) {

	tests := []struct {
		name     string
		data     [][]string
		options  PrintOptions
		expected string
		wantErr  bool
	}{
		{
			name: "table format no data",
			data: [][]string{},
			options: PrintOptions{
				ColumnLabels: []string{
					"Col A", "Col B", "Col C",
				},
			},
			wantErr: false,
			expected: `+-------+-------+-------+
| COL A | COL B | COL C |
+-------+-------+-------+
`,
		},
		{
			name: "table format",
			data: [][]string{
				{"Val 1.1", "Val 1.2", "Val 1.3"},
				{"Val 2.1", "Val 2.1", "Val 2.3"},
			},
			options: PrintOptions{
				ColumnLabels: []string{
					"Col A", "Col B", "Col C",
				},
			},
			wantErr: false,
			expected: `+---------+---------+---------+
|  COL A  |  COL B  |  COL C  |
+---------+---------+---------+
| Val 1.1 | Val 1.2 | Val 1.3 |
+---------+---------+---------+
| Val 2.1 | Val 2.1 | Val 2.3 |
+---------+---------+---------+
`,
		},
		{
			name: "table compact format no data",
			data: [][]string{},
			options: PrintOptions{
				ColumnLabels: []string{
					"Col A", "Col B", "Col C",
				},
				CompactMode: true,
			},
			wantErr: false,
			expected: `  COL A  COL B  COL C  
`,
		},
		{
			name: "table compact format",
			data: [][]string{
				{"Val 1.1", "Val 1.2", "Val 1.3"},
				{"Val 2.1", "Val 2.1", "Val 2.3"},
			},
			options: PrintOptions{
				ColumnLabels: []string{
					"Col A", "Col B", "Col C",
				},
				CompactMode: true,
			},
			wantErr: false,
			expected: `   COL A    COL B    COL C   
  Val 1.1  Val 1.2  Val 1.3  
  Val 2.1  Val 2.1  Val 2.3  
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := bytes.NewBuffer([]byte{})
			printer := NewTablePrinter(test.options)
			err := printer.PrintObj(test.data, out)
			if test.wantErr && err != nil {
				t.Errorf("Run() error = %v, wantErr %v", err, test.wantErr)
			}
			assert.Equal(t, test.expected, out.String())
		})
	}

}
