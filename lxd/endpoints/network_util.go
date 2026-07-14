package endpoints

import (
	"bytes"
	"net"
	"regexp"

	"github.com/canonical/lxd/shared/logger"
)

type networkServerErrorLogWriter struct {
	proxies []net.IP
}

// Regex for the log we want to ignore.
// Matches net/http TLS handshake reset log lines in the form:
//
//	http: TLS handshake error from <ip-or-host>:<port>: <details>: connection reset by peer
//
// It supports both:
//   - unbracketed source values (capture group 1), e.g. IPv4/hostname
//   - bracketed IPv6 source values (capture group 2, without brackets), e.g. [2001:db8::1]
var unwantedLogRegex = regexp.MustCompile(`^http: TLS handshake error from ([^\[:]+?|\[([^\]]+?)\]):[0-9]+: .+: connection reset by peer$`)

func (d networkServerErrorLogWriter) Write(p []byte) (int, error) {
	strippedLog := d.stripLog(p)
	if strippedLog == "" {
		return 0, nil
	}

	logger.Info(strippedLog)
	return len(p), nil
}

// stripLog removes the trailing newline from the log and also discards TLS
// handshake errors if they correspond to TCP probes from trusted proxy IP.
func (d networkServerErrorLogWriter) stripLog(p []byte) string {
	// Strip the newline from the end.
	p = bytes.TrimRight(p, "\n")

	// No proxies configured, nothing to filter.
	if len(d.proxies) == 0 {
		return string(p)
	}

	// Get the source IP address.
	match := unwantedLogRegex.FindSubmatch(p)
	var sourceIP string
	if match != nil {
		if match[2] != nil {
			// Inner match omits brackets of IPv6 address.
			sourceIP = string(match[2])
		} else if match[1] != nil {
			sourceIP = string(match[1])
		}
	}

	// Discard the log if the source is in our list of trusted proxies.
	if sourceIP != "" {
		parsedSourceIP := net.ParseIP(sourceIP)
		if parsedSourceIP != nil {
			for _, ip := range d.proxies {
				if ip.Equal(parsedSourceIP) {
					return ""
				}
			}
		}
	}

	return string(p)
}
