package printers

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJSONPrinter(t *testing.T) {

	tests := []struct {
		name     string
		data     [][]string
		expected string
		wantErr  bool
	}{
		{
			name:     "json format no data",
			data:     [][]string{},
			wantErr:  false,
			expected: "[]\n",
		},
		{
			name: "json array format",
			data: [][]string{
				{"Val 1.1", "Val 1.2", "Val 1.3"},
				{"Val 2.1", "Val 2.1", "Val 2.3"},
			},
			wantErr: false,
			expected: `[
    [
        "Val 1.1",
        "Val 1.2",
        "Val 1.3"
    ],
    [
        "Val 2.1",
        "Val 2.1",
        "Val 2.3"
    ]
]
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := bytes.NewBuffer([]byte{})
			printer := NewJSONPrinter()
			err := printer.PrintObj(test.data, out)
			if test.wantErr && err != nil {
				t.Errorf("Run() error = %v, wantErr %v", err, test.wantErr)
			}
			assert.Equal(t, test.expected, out.String())
		})
	}

}
