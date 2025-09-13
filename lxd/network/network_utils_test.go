package network

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
)

func Test_randomAddressInSubnet(t *testing.T) {
	tests := []struct {
		cidr     string
		wantErr  error
		wantIP   string
		validate func(ip net.IP) (bool, error)
	}{
		{
			cidr:   "198.113.14.64/32",
			wantIP: "198.113.14.64",
		},
		{
			cidr:   "198.113.14.64/31",
			wantIP: "198.113.14.64",
		},
		{
			cidr:    "198.113.14.64/30",
			wantErr: fmt.Errorf("No available addresses in subnet %q", "198.113.14.64/30"),
			validate: func(ip net.IP) (bool, error) {
				_, ok := map[string]struct{}{
					"198.113.14.66": {},
					"198.113.14.65": {},
				}[ip.String()]
				return !ok, nil
			},
		},
		{
			cidr: "198.113.14.64/30",
		},
		{
			cidr: "192.0.2.0/24",
		},
		{
			cidr: "217.237.171.249/28",
		},
		{
			cidr: "175.205.250.25/15",
		},
		{
			cidr: "37.89.212.216/6",
		},
		{
			cidr: "154.99.41.145/19",
		},
		{
			cidr: "203.136.0.41/7",
		},
		{
			cidr: "251.198.77.168/27",
		},
		{
			cidr: "37.84.172.167/15",
		},
		{
			cidr: "239.57.226.65/23",
		},
		{
			cidr: "133.247.17.201/11",
		},
		{
			cidr:   "6db5:f305:4e4a:17c9:9611:8c06:d162:dcf4/128",
			wantIP: "6db5:f305:4e4a:17c9:9611:8c06:d162:dcf4",
		},
		{
			cidr:   "6db5:f305:4e4a:17c9:9611:8c06:d162:dcf4/127",
			wantIP: "6db5:f305:4e4a:17c9:9611:8c06:d162:dcf5",
		},
		{
			cidr:    "6db5:f305:4e4a:17c9:9611:8c06:d162:dcf4/126",
			wantErr: fmt.Errorf("No available addresses in subnet %q", "6db5:f305:4e4a:17c9:9611:8c06:d162:dcf4/126"),
			validate: func(ip net.IP) (bool, error) {
				_, ok := map[string]struct{}{
					"6db5:f305:4e4a:17c9:9611:8c06:d162:dcf5": {},
					"6db5:f305:4e4a:17c9:9611:8c06:d162:dcf6": {},
					"6db5:f305:4e4a:17c9:9611:8c06:d162:dcf7": {}, // No broadcast address for IPv6.
				}[ip.String()]
				return !ok, nil
			},
		},
		{
			cidr: "6db5:f305:4e4a:17c9:9611:8c06:d162:dcf4/126",
		},
		{
			cidr: "cd14:784c:7f60:bfde:9ec1:f71f:e177:1d3b/62",
		},
		{
			cidr: "2d6b:c0a3:104e:9d64:65a7:1c8c:37c0:d0e1/47",
		},
		{
			cidr: "931f:ea66:0903:a5d2:8838:9a9e:72b8:9a62/26",
		},
		{
			cidr: "5b5a:27b4:fdf2:c85f:35d4:11e8:04c1:316f/116",
		},
		{
			cidr: "f81c:931d:8e05:9d67:1d9c:cd2c:d2fd:f445/101",
		},
		{
			cidr: "e24a:372c:66d5:7494:1aa2:7a73:b217:88b1/44",
		},
		{
			cidr: "a30f:c233:f083:552a:100c:6aae:c4a2:8419/62",
		},
		{
			cidr: "9908:0ebf:dda8:f7c7:19c5:d53f:5dcc:cb58/17",
		},
		{
			cidr: "8c50:98c3:8766:6167:3afb:a98e:4444:486a/11",
		},
		{
			cidr:    "6db5:f305:4e4a:17c9:9611:8c06:d162:dcf4/126",
			wantErr: errors.New("Forced error"),
			validate: func(ip net.IP) (bool, error) {
				return false, errors.New("Forced error")
			},
		},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("Case %d", i), func(t *testing.T) {
			var ipnet *net.IPNet
			var err error
			if i%2 == 0 {
				// ipnet.IP will have 16 bytes.
				ipnet, err = ParseIPCIDRToNet(tt.cidr)
			} else {
				// ipnet.IP will have 4 bytes.
				_, ipnet, err = net.ParseCIDR(tt.cidr)
			}

			require.NoError(t, err)

			// Use the background context if the tests have a deadline. Otherwise set a timeout of 5 seconds, with the
			// above test cases this should never fail.
			ctx := context.Background()
			cancel := func() {}
			_, ok := t.Deadline()
			if !ok {
				ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
			}

			got, gotErr := randomAddressInSubnet(ctx, *ipnet, tt.validate)
			if tt.wantErr != nil {
				assert.EqualError(t, gotErr, tt.wantErr.Error())
			} else if tt.wantIP != "" {
				assert.Equal(t, tt.wantIP, got.String())
				assert.True(t, SubnetContainsIP(ipnet, got))
			} else {
				assert.True(t, SubnetContainsIP(ipnet, got))
			}

			cancel()
		})
	}
}

func Test_randomHwaddr(t *testing.T) {
	tests := []struct {
		name string
		seed string
		want string
	}{
		{
			name: "No seed",
			seed: "",
			want: "00:16:3e:ce:9d:85",
		},
		{
			name: "Debian random seed",
			seed: "4",
			want: "00:16:3e:a5:31:ce",
		},
		{
			name: "Good enough random seed",
			seed: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			want: "00:16:3e:95:be:02",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seedNbr, _ := util.GetStableRandomGenerator(tt.seed)
			got := randomHwaddr(seedNbr)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Benchmark_randomHwaddr(b *testing.B) {
	seed := rand.New(rand.NewSource(0))
	for b.Loop() {
		_ = randomHwaddr(seed)
	}
}

func Example_complementRangesIP4() {
	_, ipnet, err := net.ParseCIDR("10.1.1.0/24")
	if err != nil {
		fmt.Printf("Err: %v\n", err)
		return
	}

	ranges := [][]*shared.IPRange{
		{
			{Start: net.ParseIP("10.1.1.1"), End: net.ParseIP("10.1.1.10")},
		},
		{
			{Start: net.ParseIP("10.1.1.10"), End: net.ParseIP("10.1.1.100")},
			{Start: net.ParseIP("10.1.1.200"), End: net.ParseIP("10.1.1.230")},
		},
		{
			{Start: net.ParseIP("10.1.1.10"), End: net.ParseIP("10.1.1.20")},
			{Start: net.ParseIP("10.1.1.15"), End: net.ParseIP("10.1.1.25")},
		},
	}

	for idx, r := range ranges {
		result, err := complementRangesIP4(r, ipnet)
		if err != nil {
			fmt.Printf("Err: %v\n", err)
			return
		}

		parts := make([]string, len(result))
		for i, r := range result {
			parts[i] = fmt.Sprintf("%s-%s", r.Start.String(), r.End.String())
		}

		fmt.Printf("Range%d: %s\n", idx+1, strings.Join(parts, ", "))
	}

	// Output:
	// Range1: 10.1.1.11-10.1.1.255
	// Range2: 10.1.1.1-10.1.1.9, 10.1.1.101-10.1.1.199, 10.1.1.231-10.1.1.255
	// Range3: 10.1.1.1-10.1.1.9, 10.1.1.26-10.1.1.255
}
