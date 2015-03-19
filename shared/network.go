package shared

import (
	"errors"
	"net"
)

func RFC3493Dialer(network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	addrs, err := net.LookupHost(host)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		c, err := net.Dial(network, net.JoinHostPort(a, port))
		if err != nil {
			continue
		}
		return c, err
	}
	return nil, errors.New("Unable to connect to: "+address)
}
