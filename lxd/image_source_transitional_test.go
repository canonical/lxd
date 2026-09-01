package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeSimpleStreamsURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "trailing slash trimmed",
			in:   "https://cloud-images.ubuntu.com/releases/",
			want: "https://cloud-images.ubuntu.com/releases",
		},
		{
			name: "path preserved",
			in:   "https://cloud-images.ubuntu.com/releases",
			want: "https://cloud-images.ubuntu.com/releases",
		},
		{
			name: "host lowercased",
			in:   "https://Cloud-Images.Ubuntu.Com/releases",
			want: "https://cloud-images.ubuntu.com/releases",
		},
		{
			name: "scheme lowercased",
			in:   "HTTPS://cloud-images.ubuntu.com/releases",
			want: "https://cloud-images.ubuntu.com/releases",
		},
		{
			name: "no path",
			in:   "https://images.lxd.canonical.com/",
			want: "https://images.lxd.canonical.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, normalizeSimpleStreamsURL(tt.in))
		})
	}
}

func TestHostPortFromServerURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{
			name: "https url with port",
			in:   "https://192.0.2.1:8443",
			want: "192.0.2.1:8443",
		},
		{
			name: "https url with path",
			in:   "https://192.0.2.1:8443/",
			want: "192.0.2.1:8443",
		},
		{
			name: "bare host and port",
			in:   "192.0.2.1:8443",
			want: "192.0.2.1:8443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := hostPortFromServerURL(tt.in)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
