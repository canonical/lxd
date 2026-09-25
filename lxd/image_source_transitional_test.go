package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/shared/api"
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
		{
			name: "default https port stripped",
			in:   "https://myimages.example.com:443",
			want: "https://myimages.example.com",
		},
		{
			name: "default https port stripped with path and trailing slash",
			in:   "https://cloud-images.ubuntu.com:443/releases/",
			want: "https://cloud-images.ubuntu.com/releases",
		},
		{
			name: "non-default port preserved",
			in:   "https://myimages.example.com:8443/streams",
			want: "https://myimages.example.com:8443/streams",
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

func TestMatchDeprecatedLXDRegistry(t *testing.T) {
	t.Parallel()

	ssProto := dbCluster.ImageRegistryProtocol(api.ImageRegistryProtocolSimpleStreams)
	lxdProto := dbCluster.ImageRegistryProtocol(api.ImageRegistryProtocolLXD)

	// Two LXD registries backed by distinct cluster links, plus a SimpleStreams registry that must
	// never be matched by this function.
	registries := []dbCluster.ImageRegistriesRow{
		{ID: 1, Name: "ss", Protocol: ssProto},
		{ID: 2, Name: "lxd-a", Protocol: lxdProto},
		{ID: 3, Name: "lxd-b", Protocol: lxdProto},
	}

	registryConfigs := map[int64]map[string]string{
		1: {"url": "https://images.example.com"},
		2: {"cluster": "link-a"},
		3: {"cluster": "link-b"},
	}

	linkIDs := map[string]int64{"link-a": 10, "link-b": 20}
	linkFingerprints := map[int64]string{10: "aaaa", 20: "bbbb"}
	linkAddresses := map[string][]string{
		"link-a": {"10.0.0.1:8443"},
		"link-b": {"10.0.0.2:8443"},
	}

	tests := []struct {
		name              string
		sourceFingerprint string
		serverAddress     string
		want              string
	}{
		{
			name:              "certificate fingerprint match",
			sourceFingerprint: "bbbb",
			serverAddress:     "unmatched.example.com:8443",
			want:              "lxd-b",
		},
		{
			name:              "certificate match preferred over address match",
			sourceFingerprint: "bbbb",
			serverAddress:     "10.0.0.1:8443",
			want:              "lxd-b",
		},
		{
			name:          "address fallback when no certificate",
			serverAddress: "10.0.0.1:8443",
			want:          "lxd-a",
		},
		{
			name:              "address fallback when certificate does not match",
			sourceFingerprint: "cccc",
			serverAddress:     "10.0.0.2:8443",
			want:              "lxd-b",
		},
		{
			name:              "no match",
			sourceFingerprint: "cccc",
			serverAddress:     "10.0.0.9:8443",
			want:              "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := matchDeprecatedLXDRegistry(registries, registryConfigs, linkIDs, linkFingerprints, linkAddresses, tt.sourceFingerprint, tt.serverAddress)
			assert.Equal(t, tt.want, got)
		})
	}
}
