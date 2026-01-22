package request

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_getAllInfo(t *testing.T) {
	tests := []struct {
		ua                        string
		expectedUserAgentProduct  UserAgentProduct
		expectedUserAgentHost     UserAgentHost
		expectedUserAgentStorage  map[string]string
		expectedUserAgentFeatures []string
		expectedError             string
	}{
		{
			ua: "LXD 5.20 (Linux; x86_64; 6.6.12; Calculate; ) (btrfs 6.7.1)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.20",
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "6.6.12",
				Distro:        "Calculate",
			},
			expectedUserAgentStorage: map[string]string{
				"btrfs": "6.7.1",
			},
		},
		{
			ua: "LXD 5.0.2 (Linux; x86_64) (cluster)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.0.2",
				LTS:     true,
			},
			expectedUserAgentHost: UserAgentHost{
				OS:   "Linux",
				Arch: "x86_64",
			},
			expectedUserAgentFeatures: []string{"cluster"},
		},
		{
			ua: "LXD 5.20 (Linux; x86_64; 5.15.0; Ubuntu; 22.04) (ceph 17.2.6; zfs 2.1.5-1ubuntu6~22.04.1) (cluster)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.20",
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "5.15.0",
				Distro:        "Ubuntu",
				DistroVersion: "22.04",
			},
			expectedUserAgentStorage: map[string]string{
				"ceph": "17.2.6",
				"zfs":  "2.1.5-1ubuntu6~22.04.1",
			},
			expectedUserAgentFeatures: []string{"cluster"},
		},
		{
			ua: "LXD 5.21 LTS (Linux; x86_64; 5.15.0; Ubuntu; 22.04) (ceph 17.2.6; zfs 2.1.5-1ubuntu6~22.04.1) (cluster)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.21",
				LTS:     true,
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "5.15.0",
				Distro:        "Ubuntu",
				DistroVersion: "22.04",
			},
			expectedUserAgentStorage: map[string]string{
				"ceph": "17.2.6",
				"zfs":  "2.1.5-1ubuntu6~22.04.1",
			},
			expectedUserAgentFeatures: []string{"cluster"},
		},
		{
			ua: "LXD 5.0.3 (Linux; x86_64; 5.15.0; Ubuntu; 22.04)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.0.3",
				LTS:     true,
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "5.15.0",
				Distro:        "Ubuntu",
				DistroVersion: "22.04",
			},
		},
		{
			ua: "LXD 5.20 (Linux; x86_64; 6.8.1; Arch Linux) (cluster)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.20",
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "6.8.1",
				Distro:        "Arch Linux",
			},
			expectedUserAgentFeatures: []string{"cluster"},
		},
		{
			ua: "LXD 5.20 (Linux; x86_64; 6.8.1; Ubuntu; 24.04) (cluster; pro)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.20",
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "6.8.1",
				Distro:        "Ubuntu",
				DistroVersion: "24.04",
			},
			expectedUserAgentFeatures: []string{"cluster", "pro"},
		},
		{
			ua: "LXD 5.20 (Linux; x86_64) (pro)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.20",
			},
			expectedUserAgentHost: UserAgentHost{
				OS:   "Linux",
				Arch: "x86_64",
			},
			expectedUserAgentFeatures: []string{"pro"},
		},
		{
			ua: "LXD 5.20 (Linux; x86_64; 6.8.1; Arch Linux) (dir 1)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.20",
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "6.8.1",
				Distro:        "Arch Linux",
			},
			expectedUserAgentStorage: map[string]string{
				"dir": "1",
			},
		},
		{
			ua: "LXD 5.20 (Linux; x86_64; 6.8.1)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.20",
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "6.8.1",
			},
		},
		{
			ua: "LXD 5.20 (Linux; x86_64)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.20",
			},
			expectedUserAgentHost: UserAgentHost{
				OS:   "Linux",
				Arch: "x86_64",
			},
		},
		{
			ua: "LXD 5.0.2-qnap5 (Linux; x86_64; 5.10.60; QTS; 5.3.0) (dir 1)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.0.2-qnap5",
				LTS:     true,
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "5.10.60",
				Distro:        "QTS",
				DistroVersion: "5.3.0",
			},
			expectedUserAgentStorage: map[string]string{
				"dir": "1",
			},
		},
		{
			ua: "LXD 5.0.2-qnap5 (Linux; x86_64; 5.10.60; Ubuntu; 18.04) (dir 1)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.0.2-qnap5",
				LTS:     true,
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "5.10.60",
				Distro:        "Ubuntu",
				DistroVersion: "18.04",
			},
			expectedUserAgentStorage: map[string]string{
				"dir": "1",
			},
		},
		{
			ua: "LXD 5.0.2-qnap5 (Linux; aarch64; 4.2.8; QTS; 5.1.6) (dir 1)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.0.2-qnap5",
				LTS:     true,
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "aarch64",
				KernelVersion: "4.2.8",
				Distro:        "QTS",
				DistroVersion: "5.1.6",
			},
			expectedUserAgentStorage: map[string]string{
				"dir": "1",
			},
		},
		{
			ua: "LXD 5.20 (Linux; x86_64; 6.6.12; Calculate; ) (btrfs     6.7.1)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.20",
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "6.6.12",
				Distro:        "Calculate",
			},
			expectedUserAgentStorage: map[string]string{
				"btrfs": "6.7.1",
			},
		},
		{
			ua: "LXD 5.21.1 LTS (Linux; x86_64; 6.6.32; Calculate; ) (btrfs 6.8.1)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.21.1",
				LTS:     true,
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "6.6.32",
				Distro:        "Calculate",
			},
			expectedUserAgentStorage: map[string]string{
				"btrfs": "6.8.1",
			},
		},
		{
			ua: "LXD 5.21.0 LTS (Linux; x86_64; 6.4.0; Debian GNU/Linux) (zfs 2.1.13-1)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.21.0",
				LTS:     true,
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "6.4.0",
				Distro:        "Debian GNU/Linux",
			},
			expectedUserAgentStorage: map[string]string{
				"zfs": "2.1.13-1",
			},
		},
		{
			ua: "LXD 5.21.0 LTS (Linux; x86_64; 6.1.0; Debian GNU/Linux; 12) (btrfs 5.16.2)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.21.0",
				LTS:     true,
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "6.1.0",
				Distro:        "Debian GNU/Linux",
				DistroVersion: "12",
			},
			expectedUserAgentStorage: map[string]string{
				"btrfs": "5.16.2",
			},
		},
		{
			ua: "LXD 5.21.0 LTS (Linux; x86_64; 6.1.0; Debian GNU/Linux; 12) (btrfs 5.16.2; lvm 2.03.11(2) (2021-01-08) / 1.02.175 (2021-01-08) / 4.48.0)",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.21.0",
				LTS:     true,
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "6.1.0",
				Distro:        "Debian GNU/Linux",
				DistroVersion: "12",
			},
			expectedUserAgentStorage: map[string]string{
				"btrfs": "5.16.2",
				"lvm":   "2.03.11(2) (2021-01-08) / 1.02.175 (2021-01-08) / 4.48.0",
			},
		},
		{
			ua: "LXD 5.20 (Linux; x86_64; 6.7.8; Ubuntu; 22.04) ()",
			expectedUserAgentProduct: UserAgentProduct{
				Name:    "LXD",
				Version: "5.20",
			},
			expectedUserAgentHost: UserAgentHost{
				OS:            "Linux",
				Arch:          "x86_64",
				KernelVersion: "6.7.8",
				Distro:        "Ubuntu",
				DistroVersion: "22.04",
			},
		},
		{
			ua:            "LXD 5.20 ())",
			expectedError: "User agent contains unbalanced parentheses",
		},
		{
			ua:            "LXD 5.20 (",
			expectedError: "User agent contains unbalanced parentheses",
		},
		{
			ua:            "LXD ()",
			expectedError: "Product does not contain a version",
		},
		{
			ua:            "LXD 5.20 beans ()",
			expectedError: "Malformed product LTS field",
		},
		{
			ua:            "LXD 5.20 LTS v2 ()",
			expectedError: `Product must be of the form "LXD <version> [LTS]"`,
		},
		{
			ua:            "LXD 5.20 () ((cluster)",
			expectedError: "User agent contains unbalanced parentheses",
		},
		{
			ua:            "LXD 5.20",
			expectedError: `User agent string must start with "<product>" and contain host information in parentheses`,
		},
		{
			ua:            "bacon 5.20 ()",
			expectedError: `Only LXD user agents are currently supported`,
		},
		{
			ua:            "LXD 5.20 () () () () () ()",
			expectedError: "User agent may contain at most two extra optional groups containing storage driver and feature details",
		},
		{
			ua:            "LXD 5.20 (Linux)",
			expectedError: "Host group must contain OS and architecture",
		},
		{
			ua:            "LXD 5.20 (Linux; x86_64; 6.7.8; Ubuntu; 22.04; Pro; LTS)",
			expectedError: "Host group cannot contain more than 5 elements",
		},
		{
			ua:            "LXD 5.20 (Linux; x86_64; 6.7.8; Ubuntu; 22.04; Pro; LTS)",
			expectedError: "Host group cannot contain more than 5 elements",
		},
		{
			ua:            "LXD 5.20 (Linux; x86_64; 6.7.8; Ubuntu; 22.04) (" + strings.Join(slices.Repeat([]string{"dir 1"}, 4), "; ") + ")",
			expectedError: `Repeated driver "dir" found in storage details`,
		},
		{
			ua:            "LXD 5.20 (Linux; x86_64; 6.7.8; Ubuntu; 22.04) () (dir 1)",
			expectedError: `Feature group may not precede storage group`,
		},
		{
			ua:            "LXD 5.20 (Linux; x86_64; 6.7.8; Ubuntu; 22.04) (dir 1) (dir 1)",
			expectedError: `Features may not contain whitespace`,
		},
		{
			ua:            "LXD 5.20 (Linux; x86_64; 6.7.8; Ubuntu; 22.04) (dir 1; cluster)",
			expectedError: `Cannot mix storage drivers and features`,
		},
		{
			ua:            "LXD 5.20 (Linux; x86_64; 6.7.8; Ubuntu; 22.04) (cluster; dir 1)",
			expectedError: `Features may not contain whitespace`,
		},
	}

	for i, test := range tests {
		t.Logf("Running test %q (case %d)", test.ua, i)
		userAgent, err := ParseUserAgent(test.ua)
		if test.expectedError == "" {
			require.NoError(t, err)
			require.Equal(t, test.expectedUserAgentProduct, userAgent.Product)
			require.Equal(t, test.expectedUserAgentHost, userAgent.Host)
			require.Equal(t, test.expectedUserAgentStorage, userAgent.Storage)
			require.Equal(t, test.expectedUserAgentFeatures, userAgent.Features)
		} else {
			require.ErrorContains(t, err, test.expectedError)
		}
	}
}
