package shared

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"
)

// IPRange defines a range of IP addresses.
// Optionally just set Start to indicate a single IP.
type IPRange struct {
	Start net.IP
	End   net.IP
}

// GetIPScope returns the scope of the IP: global, local or link.
func GetIPScope(ip string) string {
	if ip == "::1" || strings.HasPrefix(ip, "127.") {
		return "local"
	}

	if strings.HasPrefix(ip, "fe80:") || strings.HasPrefix(ip, "169.254.") {
		return "link"
	}

	return "global"
}

// ParseIPRange parses an IP range in the format "start-end" and converts it to a shared.IPRange.
// If allowedNets are supplied, then each IP in the range is checked that it belongs to at least one of them.
// IPs in the range can be zero prefixed, e.g. "::1" or "0.0.0.1", however they should not overlap with any
// supplied allowedNets prefixes. If they are within an allowed network, any zero prefixed addresses are
// returned combined with the first allowed network they are within.
// If no allowedNets supplied they are returned as-is.
func ParseIPRange(ipRange string, allowedNets ...*net.IPNet) (*IPRange, error) {
	inAllowedNet := func(ip net.IP, allowedNet *net.IPNet) net.IP {
		if ip == nil {
			return nil
		}

		ipv4 := ip.To4()

		// Only match IPv6 addresses against IPv6 networks.
		if ipv4 == nil && allowedNet.IP.To4() != nil {
			return nil
		}

		// Combine IP with network prefix if IP starts with a zero.
		// If IP is v4, then compare against 4-byte representation, otherwise use 16 byte representation.
		if (ipv4 != nil && ipv4[0] == 0) || (ipv4 == nil && ip[0] == 0) {
			allowedNet16 := allowedNet.IP.To16()
			ipCombined := make(net.IP, net.IPv6len)
			for i, b := range ip {
				ipCombined[i] = allowedNet16[i] | b
			}

			ip = ipCombined
		}

		// Check start IP is within one of the allowed networks.
		if !allowedNet.Contains(ip) {
			return nil
		}

		return ip
	}

	rangeParts := strings.SplitN(ipRange, "-", 2)
	if len(rangeParts) != 2 {
		return nil, fmt.Errorf("IP range %q must contain start and end IP addresses", ipRange)
	}

	startIP := net.ParseIP(rangeParts[0])
	endIP := net.ParseIP(rangeParts[1])

	if startIP == nil {
		return nil, fmt.Errorf("Start IP %q is invalid", rangeParts[0])
	}

	if endIP == nil {
		return nil, fmt.Errorf("End IP %q is invalid", rangeParts[1])
	}

	if bytes.Compare(startIP, endIP) > 0 {
		return nil, fmt.Errorf("Start IP %q must be less than End IP %q", startIP, endIP)
	}

	if len(allowedNets) > 0 {
		matchFound := false
		for _, allowedNet := range allowedNets {
			if allowedNet == nil {
				return nil, errors.New("Invalid allowed network")
			}

			combinedStartIP := inAllowedNet(startIP, allowedNet)
			if combinedStartIP == nil {
				continue
			}

			combinedEndIP := inAllowedNet(endIP, allowedNet)
			if combinedEndIP == nil {
				continue
			}

			// If both match then replace parsed IPs with combined IPs and stop searching.
			matchFound = true
			startIP = combinedStartIP
			endIP = combinedEndIP
			break
		}

		if !matchFound {
			return nil, fmt.Errorf("IP range %q does not fall within any of the allowed networks %v", ipRange, allowedNets)
		}
	}

	return &IPRange{
		Start: startIP,
		End:   endIP,
	}, nil
}

// ParseIPRanges parses a comma separated list of IP ranges using ParseIPRange.
func ParseIPRanges(ipRangesList string, allowedNets ...*net.IPNet) ([]*IPRange, error) {
	ipRanges := strings.Split(ipRangesList, ",")
	netIPRanges := make([]*IPRange, 0, len(ipRanges))
	for _, ipRange := range ipRanges {
		netIPRange, err := ParseIPRange(strings.TrimSpace(ipRange), allowedNets...)
		if err != nil {
			return nil, err
		}

		netIPRanges = append(netIPRanges, netIPRange)
	}

	return netIPRanges, nil
}

// ContainsIP tests whether a supplied IP falls within the IPRange.
func (r *IPRange) ContainsIP(ip net.IP) bool {
	if r.End == nil {
		// the range is only a single IP
		return r.Start.Equal(ip)
	}

	return bytes.Compare(ip, r.Start) >= 0 && bytes.Compare(ip, r.End) <= 0
}

// Overlaps checks whether two ip ranges have ip addresses in common.
func (r *IPRange) Overlaps(otherRange *IPRange) bool {
	if r.End == nil {
		return otherRange.ContainsIP(r.Start)
	}

	if otherRange.End == nil {
		return r.ContainsIP(otherRange.Start)
	}

	return r.ContainsIP(otherRange.Start) || r.ContainsIP(otherRange.End)
}

func (r *IPRange) String() string {
	if r.End == nil {
		return r.Start.String()
	}

	return fmt.Sprintf("%v-%v", r.Start, r.End)
}

// ParseNetworks parses a comma separated list of IP networks in CIDR notation.
func ParseNetworks(netList string) ([]*net.IPNet, error) {
	networks := strings.Split(netList, ",")
	ipNetworks := make([]*net.IPNet, 0, len(networks))
	for _, network := range networks {
		_, ipNet, err := net.ParseCIDR(strings.TrimSpace(network))
		if err != nil {
			return nil, err
		}

		ipNetworks = append(ipNetworks, ipNet)
	}

	return ipNetworks, nil
}
