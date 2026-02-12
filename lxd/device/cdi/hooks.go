package cdi

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

const (
	// CDIHookDefinitionKey is used to reference a CDI hook definition in a run config as a file path.
	// A CDI hook definition is a simple way to represent the symlinks to be created and the folder entries to add to the ld cache.
	// This resource file is to be read and processed by LXD's `callhook` program.
	CDIHookDefinitionKey = "cdiHookDefinitionKey"
	// CDIHooksFileSuffix is the suffix for the file that contains the CDI hooks.
	CDIHooksFileSuffix = "_cdi_hooks.json"
	// CDIConfigDevicesFileSuffix is the suffix for the file that contains the CDI config devices.
	CDIConfigDevicesFileSuffix = "_cdi_config_devices.json"
	// CDIUnixPrefix is the prefix used for creating unix char devices
	// (e.g. cdi.unix.<device_name>.<encoded_dest_path>).
	CDIUnixPrefix = "cdi.unix"
	// CDIDiskPrefix is the prefix used for creating bind mounts (or 'disk' devices)
	// representing user space files required for a CDI passthrough
	// (e.g. cdi.disk.<device_name>.<encoded_dest_path>).
	CDIDiskPrefix = "cdi.disk"
)

// SymlinkEntry represents a symlink entry.
type SymlinkEntry struct {
	Target string `json:"target" yaml:"target"`
	Link   string `json:"link" yaml:"link"`
}

// Hooks represents all the hook instructions that can be executed by
// `lxd-cdi-hook`.
type Hooks struct {
	// ContainerRootFS is the path to the container's root filesystem.
	ContainerRootFS string `json:"container_rootfs" yaml:"container_rootfs"`
	// LdCacheUpdates is a list of entries to update the ld cache.
	LDCacheUpdates []string `json:"ld_cache_updates" yaml:"ld_cache_updates"`
	// SymLinks is a list of entries to create a symlink.
	Symlinks []SymlinkEntry `json:"symlinks" yaml:"symlinks"`
}

// ConfigDevices represents devices and mounts that need to be configured from a CDI specification.
type ConfigDevices struct {
	// UnixCharDevs is a slice of unix-char device configuration.
	UnixCharDevs []map[string]string `json:"unix_char_devs" yaml:"unix_char_devs"`
	// BindMounts is a slice of mount configuration.
	BindMounts []map[string]string `json:"bind_mounts" yaml:"bind_mounts"`
}

const (
	// customCDILinkerConfFile is the name of the linker conf file we will write to
	// inside the container. The `00-lxdcdi` prefix is chosen to ensure that these libraries have
	// a higher precedence than other libraries on the system.
	customCDILinkerConfFile = "00-lxdcdi.conf"
)

// resolveTargetRelativeToLink converts a link's target into a path relative to the link's path.
func resolveTargetRelativeToLink(link string, target string) (string, error) {
	if !filepath.IsAbs(link) {
		return "", fmt.Errorf("The link must be an absolute path: %q (target: %q)", link, target)
	}

	// If target is already relative, return as-is.
	if !filepath.IsAbs(target) {
		return target, nil
	}

	// Clean both paths to normalize them.
	linkClean := filepath.Clean(link)
	targetClean := filepath.Clean(target)

	linkDir := filepath.Dir(linkClean)

	// Calculate the relative path from link's directory to the target.
	relPath, err := filepath.Rel(linkDir, targetClean)
	if err != nil {
		return "", err
	}

	return relPath, nil
}

// ApplyHooksToContainer applies CDI hooks to a container by creating symlinks
// and updating the linker cache. This function can be called both during
// container start (from LXC hook) and during hotplug.
func ApplyHooksToContainer(hooksFilePath string, containerRootFS string) error {
	hookFile, err := os.Open(hooksFilePath)
	if err != nil {
		return fmt.Errorf("Failed opening the CDI hooks file at %q: %w", hooksFilePath, err)
	}

	defer hookFile.Close()

	hooks := &Hooks{}
	err = json.NewDecoder(hookFile).Decode(hooks)
	if err != nil {
		return fmt.Errorf("Failed decoding the CDI hooks file at %q: %w", hooksFilePath, err)
	}

	// Creating the symlinks
	for _, symlink := range hooks.Symlinks {
		// Resolve hook link from target
		target, err := resolveTargetRelativeToLink(symlink.Link, symlink.Target)
		if err != nil {
			return fmt.Errorf("Failed resolving a CDI symlink: %w", err)
		}

		// Try to create the directory if it doesn't exist
		err = os.MkdirAll(filepath.Dir(filepath.Join(containerRootFS, symlink.Link)), 0755)
		if err != nil {
			return fmt.Errorf("Failed creating the directory for the CDI symlink: %w", err)
		}

		// Create the symlink
		err = os.Symlink(target, filepath.Join(containerRootFS, symlink.Link))
		if err != nil {
			if !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("Failed creating the CDI symlink: %w", err)
			}
		}
	}

	// Updating the linker cache
	ln := len(hooks.LDCacheUpdates)
	if ln > 0 {
		ldConfDirPath := filepath.Join(containerRootFS, "etc", "ld.so.conf.d")
		err = os.MkdirAll(ldConfDirPath, 0755)
		if err != nil {
			return fmt.Errorf("Failed creating the linker conf directory at %q: %w", ldConfDirPath, err)
		}

		ldConfFilePath := containerRootFS + "/etc/ld.so.conf.d/" + customCDILinkerConfFile
		_, err = os.Stat(ldConfFilePath)
		if err == nil {
			// The file already exists. Read it first, analyze its entries
			// and add the ones that are not already there.
			ldConfFile, err := os.OpenFile(ldConfFilePath, os.O_APPEND|os.O_RDWR, 0644)
			if err != nil {
				return fmt.Errorf("Failed opening the ld.so.conf file at %q: %w", ldConfFilePath, err)
			}

			existingLinkerEntries := make(map[string]bool)
			scanner := bufio.NewScanner(ldConfFile)
			for scanner.Scan() {
				existingLinkerEntries[strings.TrimSpace(scanner.Text())] = true
			}

			for _, update := range hooks.LDCacheUpdates {
				if !existingLinkerEntries[update] {
					_, err = fmt.Fprintln(ldConfFile, update)
					if err != nil {
						ldConfFile.Close()
						return fmt.Errorf("Failed writing to the linker conf file at %q: %w", ldConfFilePath, err)
					}

					existingLinkerEntries[update] = true
				}
			}

			ldConfFile.Close()
		} else if errors.Is(err, os.ErrNotExist) {
			// The file does not exist. We simply create it with our entries.
			ldConfFile, err := os.OpenFile(ldConfFilePath, os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return fmt.Errorf("Failed creating the linker conf file at %q: %w", ldConfFilePath, err)
			}

			for _, update := range hooks.LDCacheUpdates {
				_, err = fmt.Fprintln(ldConfFile, update)
				if err != nil {
					ldConfFile.Close()
					return fmt.Errorf("Failed writing to the linker conf file at %q: %w", ldConfFilePath, err)
				}
			}

			ldConfFile.Close()
		} else {
			return fmt.Errorf("Could not stat the linker conf file to add CDI linker entries at %q: %w", ldConfFilePath, err)
		}
	}
	return nil
}

// UpdateLDCache updates the linker cache inside the instance.
func UpdateLDCache(inst instance.Instance) {
	l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
	cmd, err := inst.Exec(api.InstanceExecPost{
		Command:   []string{"/sbin/ldconfig"},
		WaitForWS: false,
	}, nil, nil, nil)

	if err != nil {
		l.Warn("Failed executing ldconfig in the container", logger.Ctx{"error": err})
		return
	}

	p, err := cmd.Wait()
	if err != nil {
		l.Warn("Failed executing ldconfig in the container", logger.Ctx{"error": err, "exit code": p})
	}
}
