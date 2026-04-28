package api

import (
	"testing"
)

func TestIsTransitionalSimpleStreamsURL(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		expect bool
	}{
		{
			name:   "Ubuntu releases",
			url:    "https://cloud-images.ubuntu.com/releases/",
			expect: true,
		},
		{
			name:   "Ubuntu buildd releases",
			url:    "https://cloud-images.ubuntu.com/buildd/releases/",
			expect: true,
		},
		{
			name:   "Ubuntu daily",
			url:    "https://cloud-images.ubuntu.com/daily/",
			expect: true,
		},
		{
			name:   "LXD images",
			url:    "https://images.lxd.canonical.com/",
			expect: true,
		},
		{
			name:   "LXD images no trailing slash",
			url:    "https://images.lxd.canonical.com",
			expect: true,
		},
		{
			name:   "HTTP scheme rejected",
			url:    "http://cloud-images.ubuntu.com/releases/",
			expect: false,
		},
		{
			name:   "Unknown host rejected",
			url:    "https://example.com/releases/",
			expect: false,
		},
		{
			name:   "Empty string",
			url:    "",
			expect: false,
		},
		{
			name:   "Invalid URL",
			url:    "://not-a-url",
			expect: false,
		},
		{
			name:   "Case insensitive host",
			url:    "https://Cloud-Images.Ubuntu.Com/releases/",
			expect: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsTransitionalSimpleStreamsURL(tc.url)
			if got != tc.expect {
				t.Errorf("IsTransitionalSimpleStreamsURL(%q) = %v, want %v", tc.url, got, tc.expect)
			}
		})
	}
}

func TestTransitionalRegistryName(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		expect string
	}{
		{
			name:   "Ubuntu releases",
			url:    "https://cloud-images.ubuntu.com/releases/",
			expect: "cloud-images.ubuntu.com-releases",
		},
		{
			name:   "Ubuntu buildd releases",
			url:    "https://cloud-images.ubuntu.com/buildd/releases/",
			expect: "cloud-images.ubuntu.com-buildd-releases",
		},
		{
			name:   "LXD images with trailing slash",
			url:    "https://images.lxd.canonical.com/",
			expect: "images.lxd.canonical.com",
		},
		{
			name:   "LXD images without trailing slash",
			url:    "https://images.lxd.canonical.com",
			expect: "images.lxd.canonical.com",
		},
		{
			name:   "Case normalized",
			url:    "https://Cloud-Images.Ubuntu.Com/Releases/",
			expect: "cloud-images.ubuntu.com-Releases",
		},
		{
			name:   "Multiple path segments",
			url:    "https://cloud-images.ubuntu.com/a/b/c/",
			expect: "cloud-images.ubuntu.com-a-b-c",
		},
		{
			name:   "Empty string returns empty",
			url:    "",
			expect: "",
		},
		{
			name:   "No host returns empty",
			url:    "https://",
			expect: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TransitionalRegistryName(tc.url)
			if got != tc.expect {
				t.Errorf("TransitionalRegistryName(%q) = %q, want %q", tc.url, got, tc.expect)
			}
		})
	}
}
