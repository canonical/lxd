package shared

import (
	"bytes"
	"fmt"
	"net"
)

// IPRange defines a range of IP addresses.
// Optionally just set Start to indicate a single IP.
type IPRange struct {
	Start net.IP
	End   net.IP
}

// ContainsIP tests whether a supplied IP falls within the IPRange.
func (r *IPRange) ContainsIP(ip net.IP) bool {
	if r.End == nil {
		// the range is only a single IP
		return r.Start.Equal(ip)
	}

	return bytes.Compare(ip, r.Start) >= 0 && bytes.Compare(ip, r.End) <= 0
}

func (r *IPRange) String() string {
	if r.End == nil {
		return r.Start.String()
	}

	return fmt.Sprintf("%v-%v", r.Start, r.End)
}
