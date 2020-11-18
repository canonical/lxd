package drivers

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"

	"github.com/grant-he/lxd/shared"
)

// fsExists checks that the Ceph FS instance indeed exists.
func (d *cephfs) fsExists(clusterName string, userName string, fsName string) bool {
	_, err := shared.RunCommand("ceph", "--name", fmt.Sprintf("client.%s", userName), "--cluster", clusterName, "fs", "get", fsName)
	if err != nil {
		return false
	}

	return true
}

// getConfig parses the Ceph configuration file and returns the list of monitors and secret key.
func (d *cephfs) getConfig(clusterName string, userName string) ([]string, string, error) {
	// Parse the CEPH configuration.
	cephConf, err := os.Open(fmt.Sprintf("/etc/ceph/%s.conf", clusterName))
	if err != nil {
		return nil, "", errors.Wrapf(err, "Failed to open '%s", fmt.Sprintf("/etc/ceph/%s.conf", clusterName))
	}

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
		return nil, "", fmt.Errorf("Couldn't find a CPEH mon")
	}

	// Parse the CEPH keyring.
	cephKeyring, err := os.Open(fmt.Sprintf("/etc/ceph/%v.client.%v.keyring", clusterName, userName))
	if err != nil {
		return nil, "", errors.Wrapf(err, "Failed to open '%s", fmt.Sprintf("/etc/ceph/%v.client.%v.keyring", clusterName, userName))
	}

	var cephSecret string

	scan = bufio.NewScanner(cephKeyring)
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
		return nil, "", fmt.Errorf("Couldn't find a keyring entry")
	}

	return cephMon, cephSecret, nil
}
