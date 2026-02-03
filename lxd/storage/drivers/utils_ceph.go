package drivers

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// CephGetRBDImageName returns the RBD image name as it is used in ceph.
//
// Separate the snapshot component because of the two ways LXD uses Ceph:
//   - The `rbd` cli utility
//   - Via Qemu QMP
//
// The rbd utility requires the snapshot's name to be appended to the volume
// name with an '@'. QMP expects a snapshot name to be passed as a separate
// parameter.
//
// Example:
// A custom block volume named vol1 in project default will return custom_default_vol1.block.
func CephGetRBDImageName(vol Volume, zombie bool) (imageName string, snapName string) {
	parentName, snapName, isSnapshot := api.GetParentAndSnapshotName(vol.name)

	if isSnapshot {
		snapName = "snapshot_" + snapName
	}

	// Only use filesystem suffix on filesystem type image volumes (for all content types).
	if vol.volType == VolumeTypeImage || vol.volType == cephVolumeTypeZombieImage {
		parentName = parentName + "_" + vol.ConfigBlockFilesystem()
	}

	switch vol.contentType {
	case ContentTypeBlock:
		parentName = parentName + cephBlockVolSuffix
	case ContentTypeISO:
		parentName = parentName + cephISOVolSuffix
	}

	// Use volume's type as storage volume prefix, unless there is an override in cephVolTypePrefixes.
	volumeTypePrefix := string(vol.volType)
	volumeTypePrefixOverride, foundOveride := cephVolTypePrefixes[vol.volType]
	if foundOveride {
		volumeTypePrefix = volumeTypePrefixOverride
	}

	imageName = volumeTypePrefix + "_" + parentName

	// If the volume is to be in zombie state (i.e. not tracked by the LXD database),
	// prefix the output with "zombie_".
	if zombie {
		imageName = "zombie_" + imageName
	}

	return imageName, snapName
}

// CephMonitors gets the mon-host field for the relevant cluster and extracts the list of addresses and ports.
func CephMonitors(cluster string) ([]string, error) {
	// Open the CEPH configuration.
	cephConf, err := os.Open("/etc/ceph/" + cluster + ".conf")
	if err != nil {
		return nil, fmt.Errorf("Failed to open %q: %w", "/etc/ceph/"+cluster+".conf", err)
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
			// It supports a space separate list of comma separated lists of:
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
			entries := strings.SplitSeq(fields[1], " ")
			for entry := range entries {
				servers := strings.SplitSeq(entry, ",")
				for server := range servers {
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
					server = strings.ReplaceAll(server, "]]", "]")
					if !strings.HasPrefix(server, "[") {
						server = strings.TrimSuffix(server, "]")
					}

					// Trim any spaces.
					server = strings.TrimSpace(server)

					// If nothing left, skip.
					if server == "" {
						continue
					}

					// Append the default v1 port if none are present.
					if !strings.HasSuffix(server, ":6789") && !strings.HasSuffix(server, ":3300") {
						server += ":6789"
					}

					cephMon = append(cephMon, strings.TrimSpace(server))
				}
			}
		}
	}

	if len(cephMon) == 0 {
		return nil, errors.New("Couldn't find a CEPH mon")
	}

	return cephMon, nil
}

func getCephKeyFromFile(path string) (string, error) {
	cephKeyring, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("Failed to open %q: %w", path, err)
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
		return "", errors.New("Couldn't find a keyring entry")
	}

	return cephSecret, nil
}

// CephKeyring gets the key for a particular Ceph cluster and client name.
func CephKeyring(cluster string, client string) (string, error) {
	var cephSecret string
	cephConfigPath := "/etc/ceph/" + cluster + ".conf"

	keyringPathFull := "/etc/ceph/" + cluster + ".client." + client + ".keyring"
	keyringPathCluster := "/etc/ceph/" + cluster + ".keyring"
	keyringPathGlobal := "/etc/ceph/keyring"
	keyringPathGlobalBin := "/etc/ceph/keyring.bin"

	if shared.PathExists(keyringPathFull) {
		return getCephKeyFromFile(keyringPathFull)
	} else if shared.PathExists(keyringPathCluster) {
		return getCephKeyFromFile(keyringPathCluster)
	} else if shared.PathExists(keyringPathGlobal) {
		return getCephKeyFromFile(keyringPathGlobal)
	} else if shared.PathExists(keyringPathGlobalBin) {
		return getCephKeyFromFile(keyringPathGlobalBin)
	} else if shared.PathExists(cephConfigPath) {
		// Open the CEPH config file.
		cephConfig, err := os.Open(cephConfigPath)
		if err != nil {
			return "", fmt.Errorf("Failed to open %q: %w", cephConfigPath, err)
		}

		// Locate the keyring entry and its value.
		scan := bufio.NewScanner(cephConfig)
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

				// Check all key related config keys.
				switch strings.TrimSpace(fields[0]) {
				case "key":
					cephSecret = strings.TrimSpace(fields[1])
				case "keyfile":
					key, err := os.ReadFile(fields[1])
					if err != nil {
						return "", err
					}

					cephSecret = strings.TrimSpace(string(key))
				case "keyring":
					return getCephKeyFromFile(strings.TrimSpace(fields[1]))
				}
			}

			if cephSecret != "" {
				break
			}
		}
	}

	if cephSecret == "" {
		return "", errors.New("Couldn't find a keyring entry")
	}

	return cephSecret, nil
}

// cephCLIVersion returns the version of a given ceph CLI command.
func cephCLIVersion(command string) (string, error) {
	out, err := shared.RunCommandCLocale(command, "--version")
	if err != nil {
		return "", err
	}

	out = strings.TrimSpace(out)
	fields := strings.Split(out, " ")
	if strings.HasPrefix(out, "ceph version ") && len(fields) > 2 {
		return fields[2], nil
	}

	if out == "" {
		return "", fmt.Errorf("Empty %s version output", command)
	}

	return out, nil
}

// rbdVersion returns the RBD version.
func rbdVersion() (string, error) {
	return cephCLIVersion("rbd")
}

// radosgwVersion returns the radosgw-admin version.
func radosgwVersion() (string, error) {
	return cephCLIVersion("radosgw-admin")
}
