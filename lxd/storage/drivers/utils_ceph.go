package drivers

import (
	"bufio"
	"context"
	"encoding/json"
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

// callCeph makes a call to ceph with the given args.
func callCeph(ctx context.Context, args ...string) (string, error) {
	out, err := shared.RunCommand(ctx, "ceph", args...)
	return strings.TrimSpace(out), err
}

// callCephJSON makes a call to the ceph admin tool with the given args then parses the JSON output into out.
func callCephJSON(ctx context.Context, out any, args ...string) error {
	// Get as JSON format.
	args = append([]string{"--format", "json"}, args...)

	// Make the call.
	jsonOut, err := callCeph(ctx, args...)
	if err != nil {
		return err
	}

	// Parse the JSON.
	return json.Unmarshal([]byte(jsonOut), out)
}

// Monitors holds a list of Ceph monitor addresses based on which protocol they expect.
type Monitors struct {
	V1 []string
	V2 []string
}

// CephMonitors returns a list of public monitor addresses for the given cluster.
func CephMonitors(ctx context.Context, cluster string) (Monitors, error) {
	// Get the monitor dump.
	monitors := struct {
		Mons []struct {
			PublicAddrs struct {
				Addrvec []struct {
					Type string `json:"type"`
					Addr string `json:"addr"`
				} `json:"addrvec"`
			} `json:"public_addrs"`
		} `json:"mons"`
	}{}

	err := callCephJSON(ctx, &monitors, "--cluster", cluster, "mon", "dump")
	if err != nil {
		return Monitors{}, fmt.Errorf("Ceph mon dump for %q failed: %w", cluster, err)
	}

	// Loop through monitors then monitor addresses and add them to the list.
	var ep Monitors
	for _, mon := range monitors.Mons {
		for _, addr := range mon.PublicAddrs.Addrvec {
			switch addr.Type {
			case "v1":
				ep.V1 = append(ep.V1, addr.Addr)
			case "v2":
				ep.V2 = append(ep.V2, addr.Addr)
			}
		}
	}

	if len(ep.V2) == 0 {
		if len(ep.V1) == 0 {
			return Monitors{}, fmt.Errorf("No Ceph monitors found for %q", cluster)
		}
	}

	return ep, nil
}

// CephFSID retrieves the FSID for the given cluster.
func CephFSID(ctx context.Context, cluster string) (string, error) {
	fsid := struct {
		FSID string `json:"fsid"`
	}{}

	err := callCephJSON(ctx, &fsid, "--cluster", cluster, "fsid")
	if err != nil {
		return "", fmt.Errorf("Failed getting fsid for %q: %w", cluster, err)
	}

	return fsid.FSID, nil
}

// CephBuildMount creates a mount string and option list from mount parameters.
func CephBuildMount(user string, key string, fsid string, monitors Monitors, fsName string, path string, msMode string) (source string, options []string) {
	// Ceph mount paths must begin with a '/'. If it doesn't (or is empty),
	// prefix it now.
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Prefer V2 addresses when available; fall back to V1.
	monAddrs := monitors.V1
	if len(monitors.V2) > 0 {
		monAddrs = monitors.V2
	}

	// Build the source path.
	source = fmt.Sprintf("%s@%s.%s=%s", user, fsid, fsName, path)

	// Build the options list.
	options = []string{
		"mon_addr=" + strings.Join(monAddrs, "/"),
		"name=" + user,
	}

	// If key is blank assume cephx is disabled.
	if key != "" {
		options = append(options, "secret="+key)
	}

	options = append(options, "ms_mode="+msMode)

	return source, options
}

// CephMSMode queries the cluster for the client messenger mode and maps it to
// the equivalent kernel ms_mode mount option. The Ceph config key
// ms_client_mode is a space-separated preference list (e.g. "crc secure");
// the first entry is the preferred mode which maps to a kernel "prefer-*"
// variant.
func CephMSMode(ctx context.Context, cluster string) (string, error) {
	raw, err := callCeph(ctx, "--cluster", cluster, "config", "get", "client", "ms_client_mode")
	if err != nil {
		return "", fmt.Errorf("Failed querying ms_client_mode for %q: %w", cluster, err)
	}

	modes := strings.Fields(raw)
	if len(modes) == 0 {
		return "prefer-crc", nil
	}

	// Single mode means no fallback — use it as-is (e.g. "secure" or "crc").
	if len(modes) == 1 {
		return modes[0], nil
	}

	// Multiple modes — the first one is preferred, map to kernel "prefer-*".
	return "prefer-" + modes[0], nil
}

func getCephKeyFromFile(path string) (string, error) {
	cephKeyring, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("Failed opening %q: %w", path, err)
	}

	defer func() { _ = cephKeyring.Close() }()

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
		return "", errors.New("Could not find a keyring entry")
	}

	return cephSecret, nil
}

// CephKeyring gets the key for a particular Ceph cluster and client name.
func CephKeyring(ctx context.Context, cluster string, client string) (string, error) {
	// Try to find the key from the filesystem directly (fast path).
	value, err := cephKeyringFromFile(cluster, client)
	if err == nil {
		return value, nil
	}

	// Fall back to using the ceph CLI.

	// If client isn't prefixed, prefix it with 'client.'.
	cephClient := client
	if !strings.Contains(cephClient, ".") {
		cephClient = "client." + cephClient
	}

	// Check that cephx is enabled.
	authType, err := callCeph(ctx, "--cluster", cluster, "config", "get", cephClient, "auth_service_required")
	if err != nil {
		return "", fmt.Errorf("Failed querying Ceph config for auth_service_required: %w", err)
	}

	if authType == "none" {
		return "", nil
	}

	// Call ceph auth get-key.
	key := struct {
		Key string `json:"key"`
	}{}

	err = callCephJSON(ctx, &key, "--cluster", cluster, "auth", "get-key", cephClient)
	if err != nil {
		return "", fmt.Errorf("Failed getting keyring for %q on %q: %w", client, cluster, err)
	}

	return key.Key, nil
}

// cephKeyringFromFile gets the key for a particular Ceph cluster and client name from local files.
func cephKeyringFromFile(cluster string, client string) (string, error) {
	var cephSecret string
	cephConfigPath := "/etc/ceph/" + cluster + ".conf"

	keyringPathFull := "/etc/ceph/" + cluster + ".client." + client + ".keyring"
	keyringPathCluster := "/etc/ceph/" + cluster + ".keyring"
	keyringPathGlobal := "/etc/ceph/keyring"
	keyringPathGlobalBin := "/etc/ceph/keyring.bin"

	// Try keyring files in order of specificity.
	for _, keyringPath := range []string{
		keyringPathFull,
		keyringPathCluster,
		keyringPathGlobal,
		keyringPathGlobalBin,
	} {
		secret, err := getCephKeyFromFile(keyringPath)
		if err == nil {
			return secret, nil
		}

		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}

	// Fall back to parsing the Ceph config file.
	cephConfig, err := os.Open(cephConfigPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("Failed opening %q: %w", cephConfigPath, err)
	}

	if err == nil {
		defer func() { _ = cephConfig.Close() }()

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
		return "", errors.New("Could not find a keyring entry")
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
