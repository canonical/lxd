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
		{
			name: "mon_host with IPs and some ports",
			line: "mon_host=192.0.2.1:6789,192.0.2.2:3300,192.0.2.3",
			want: []string{"192.0.2.1:6789", "192.0.2.2:3300", "192.0.2.3:6789"},
		},
		{
			name: "mon host with DNS names and some ports",
			line: "mon host = foo.example.com:3300,bar.example.com:6789,baz.example.com",
			want: []string{"foo.example.com:3300", "bar.example.com:6789", "baz.example.com:6789"},
		},
		{
			name: "mon host with messenger versions",
			line: "mon host = v1:192.0.2.1:6789,[v1:192.0.2.2],v2:192.0.2.3,[v2:192.0.2.4]",
			want: []string{"192.0.2.1:6789", "192.0.2.2:6789", "192.0.2.3:3300", "192.0.2.4:3300"},
		},
		{
			name: "mon host with some messenger versions",
			line: "mon host = v1:192.0.2.1:6789,v2:192.0.2.2,192.0.2.3",
			want: []string{"192.0.2.1:6789", "192.0.2.2:3300", "192.0.2.3:6789"},
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
