package drivers

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// CephMonitors gets the mon-host field for the relevant cluster and extracts the list of addresses and ports.
func CephMonitors(cluster string) ([]string, error) {
	// Open the CEPH configuration.
	cephConf, err := os.Open(fmt.Sprintf("/etc/ceph/%s.conf", cluster))
	if err != nil {
		return nil, fmt.Errorf("Failed to open %q: %w", fmt.Sprintf("/etc/ceph/%s.conf", cluster), err)
	}

	// Locate the mon-host key and its values.
	cephMon := []string{}
	scan := bufio.NewScanner(cephConf)
	for scan.Scan() {
		line := scan.Text()
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "mon_host") || strings.HasPrefix(line, "mon-host") || strings.HasPrefix(line, "mon host") {
			fields := strings.SplitN(line, "=", 2)
			if len(fields) < 2 {
				continue
			}

			// Parsing mon_host is quite tricky.
			// It supports a comma separated list of:
			//  - DNS names
			//  - IPv4 addresses
			//  - IPv6 addresses (square brackets)
			//  - Optional version indicator
			//  - Optional port numbers
			//  - Optional data (after / separator)
			//  - Tuples of addresses with all the above still applying inside the tuple
			//
			// As this function is primarily used for cephfs which
			// doesn't take the version indication, trailing bits or supports those
			// tuples, all of those effectively get stripped away to get a clean
			// address list (with ports).
			servers := strings.Split(fields[1], ",")
			for _, server := range servers {
				// Trim leading/trailing spaces.
				server = strings.TrimSpace(server)

				// Trim leading protocol version.
				server = strings.TrimPrefix(server, "v1:")
				server = strings.TrimPrefix(server, "v2:")
				server = strings.TrimPrefix(server, "[v1:")
				server = strings.TrimPrefix(server, "[v2:")

				// Trim trailing divider.
				server = strings.Split(server, "/")[0]

				// Handle end of nested blocks.
				server = strings.Replace(server, "]]", "]", 0)
				if !strings.HasPrefix(server, "[") {
					server = strings.TrimSuffix(server, "]")
				}

				cephMon = append(cephMon, strings.TrimSpace(server))
			}
			break
		}
	}

	if len(cephMon) == 0 {
		return nil, fmt.Errorf("Couldn't find a CPEH mon")
	}

	return cephMon, nil
}

// CephKeyring gets the key for a particular Ceph cluster and client name.
func CephKeyring(cluster string, client string) (string, error) {
	// Open the CEPH keyring.
	cephKeyring, err := os.Open(fmt.Sprintf("/etc/ceph/%v.client.%v.keyring", cluster, client))
	if err != nil {
		return "", fmt.Errorf("Failed to open %q: %w", fmt.Sprintf("/etc/ceph/%v.client.%v.keyring", cluster, client), err)
	}

	// Locate the keyring entry and its value.
	var cephSecret string
	scan := bufio.NewScanner(cephKeyring)
	for scan.Scan() {
		line := scan.Text()
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "key") {
			fields := strings.SplitN(line, "=", 2)
			if len(fields) < 2 {
				continue
			}

			cephSecret = strings.TrimSpace(fields[1])
			break
		}
	}

	if cephSecret == "" {
		return "", fmt.Errorf("Couldn't find a keyring entry")
	}

	return cephSecret, nil
}
