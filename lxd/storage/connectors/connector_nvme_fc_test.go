package connectors

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_nvmeFCMissingPaths(t *testing.T) {
	const targetA = "nn-0x20000024ff123456:pn-0x21000024ff123456"
	const targetB = "nn-0x20000024ff654321:pn-0x21000024ff654321"

	const hbaA = "nn-0x20000090fae0b99a:pn-0x10000090fae0b99a"
	const hbaB = "nn-0x20000090fae0b99b:pn-0x10000090fae0b99b"

	tests := []struct {
		name       string
		session    *session
		targetAddr string
		hostAddrs  []string
		want       []string
	}{
		{
			name:       "No session yet connects through every HBA",
			session:    nil,
			targetAddr: targetA,
			hostAddrs:  []string{hbaA, hbaB},
			want:       []string{hbaA, hbaB},
		},
		{
			name:       "Session without any path to the target port",
			session:    &session{hostAddresses: map[string][]string{}},
			targetAddr: targetA,
			hostAddrs:  []string{hbaA, hbaB},
			want:       []string{hbaA, hbaB},
		},
		{
			name:       "Session covering only one HBA still connects the other",
			session:    &session{hostAddresses: map[string][]string{targetA: {hbaA}}},
			targetAddr: targetA,
			hostAddrs:  []string{hbaA, hbaB},
			want:       []string{hbaB},
		},
		{
			name:       "Session covering every HBA connects nothing",
			session:    &session{hostAddresses: map[string][]string{targetA: {hbaA, hbaB}}},
			targetAddr: targetA,
			hostAddrs:  []string{hbaA, hbaB},
			want:       []string{},
		},
		{
			name:       "Paths of another target port are not considered",
			session:    &session{hostAddresses: map[string][]string{targetB: {hbaA, hbaB}}},
			targetAddr: targetA,
			hostAddrs:  []string{hbaA, hbaB},
			want:       []string{hbaA, hbaB},
		},
		{
			name:       "Target addresses are matched case insensitively",
			session:    &session{hostAddresses: map[string][]string{strings.ToUpper(targetA): {hbaA}}},
			targetAddr: targetA,
			hostAddrs:  []string{hbaA, hbaB},
			want:       []string{hbaB},
		},
		{
			name:       "Host addresses are matched case insensitively",
			session:    &session{hostAddresses: map[string][]string{targetA: {strings.ToUpper(hbaA)}}},
			targetAddr: targetA,
			hostAddrs:  []string{hbaA, hbaB},
			want:       []string{hbaB},
		},
		{
			name:       "Session with a path through an HBA that is no longer online",
			session:    &session{hostAddresses: map[string][]string{targetA: {hbaA, hbaB}}},
			targetAddr: targetA,
			hostAddrs:  []string{hbaA},
			want:       []string{},
		},
		{
			name:       "No online HBAs",
			session:    &session{hostAddresses: map[string][]string{targetA: {hbaA}}},
			targetAddr: targetA,
			hostAddrs:  nil,
			want:       []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, nvmeFCMissingPaths(test.session, test.targetAddr, test.hostAddrs))
		})
	}
}

func Test_nvmeFCTransportAddress(t *testing.T) {
	tests := []struct {
		name     string
		nodeName string
		portName string
		want     string
	}{
		{
			name:     "Plain sysfs values",
			nodeName: "0x20000024ff123456",
			portName: "0x21000024ff123456",
			want:     "nn-0x20000024ff123456:pn-0x21000024ff123456",
		},
		{
			name:     "Trailing newlines from sysfs read",
			nodeName: "0x20000024ff123456\n",
			portName: "0x21000024ff123456\n",
			want:     "nn-0x20000024ff123456:pn-0x21000024ff123456",
		},
		{
			name:     "Surrounding whitespace",
			nodeName: "  0x20000024ff123456  ",
			portName: "  0x21000024ff123456  ",
			want:     "nn-0x20000024ff123456:pn-0x21000024ff123456",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, nvmeFCTransportAddress(test.nodeName, test.portName))
		})
	}
}
