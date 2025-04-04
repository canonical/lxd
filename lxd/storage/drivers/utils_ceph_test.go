package drivers

import (
	"slices"
	"testing"
)

func Test_parseCephMonHost(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []string
	}{
		{
			name: "Invalid mon host",
			line: "mon host: 192.0.2.1,192.0.2.2,192.0.2.3",
			want: []string{},
		},
		{
			name: "mon host with IPs",
			line: "mon host = 192.0.2.1,192.0.2.2,192.0.2.3",
			want: []string{"192.0.2.1:6789", "192.0.2.2:6789", "192.0.2.3:6789"},
		},
		{
			name: "mon host with IPs and spaces",
			line: "mon host = 192.0.2.1, 192.0.2.2, 192.0.2.3",
			want: []string{"192.0.2.1:6789", "192.0.2.2:6789", "192.0.2.3:6789"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCephMonHost(tt.line)
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
