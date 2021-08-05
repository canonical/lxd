package drivers

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"
)

// CephMonitors gets the mon-host field for the relevant cluster and extracts the list of addresses and ports.
func CephMonitors(cluster string) ([]string, error) {
	// Open the CEPH configuration.
	cephConf, err := os.Open(fmt.Sprintf("/etc/ceph/%s.conf", cluster))
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to open %q", fmt.Sprintf("/etc/ceph/%s.conf", cluster))
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

			servers := strings.Split(fields[1], ",")
			for _, server := range servers {
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
		return "", errors.Wrapf(err, "Failed to open %q", fmt.Sprintf("/etc/ceph/%v.client.%v.keyring", cluster, client))
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
