package cdi

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

const (
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

type containerFS interface {
	MkdirAll(path string) error
	Symlink(oldname, newname string) error
	OpenFile(path string, flags int) (io.ReadWriteCloser, error)
}

type sftpContainerFS struct {
	client *sftp.Client
}

// MkdirAll creates a directory named path, along with any necessary parents.
func (s *sftpContainerFS) MkdirAll(path string) error { return s.client.MkdirAll(path) }

// Symlink creates newname as a symbolic link to oldname.
func (s *sftpContainerFS) Symlink(oldname, newname string) error {
	return s.client.Symlink(oldname, newname)
}

// OpenFile opens the named file with the specified flags.
func (s *sftpContainerFS) OpenFile(path string, flags int) (io.ReadWriteCloser, error) {
	return s.client.OpenFile(path, flags)
}

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
// and updating the linker configuration using SFTP.
func ApplyHooksToContainer(hooksFilePath string, inst instance.Instance) error {
	sftpClient, err := inst.FileSFTP()
	if err != nil {
		return fmt.Errorf("Failed getting SFTP client: %w", err)
	}

	defer func() { _ = sftpClient.Close() }()

	err = applyHooksWithFS(hooksFilePath, &sftpContainerFS{client: sftpClient})
	if err != nil {
		return err
	}

	updateLDCache(inst, &sftpContainerFS{client: sftpClient})

	return nil
}

// applyHooksWithFS is the testable core of ApplyHooksToContainer.
// It applies CDI hooks using the provided containerFS implementation.
func applyHooksWithFS(hooksFilePath string, cfs containerFS) error {
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
		linkDir := filepath.Dir(symlink.Link)
		err = cfs.MkdirAll(linkDir)
		if err != nil {
			return fmt.Errorf("Failed creating the directory for the CDI symlink: %w", err)
		}

		// Create the symlink
		err = cfs.Symlink(target, symlink.Link)
		if err != nil {
			if !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("Failed creating the CDI symlink: %w", err)
			}
		}
	}

	// Updating the linker configuration.
	if len(hooks.LDCacheUpdates) > 0 {
		ldConfDirPath := "/etc/ld.so.conf.d"
		err = cfs.MkdirAll(ldConfDirPath)
		if err != nil {
			return fmt.Errorf("Failed creating the linker conf directory at %q: %w", ldConfDirPath, err)
		}

		ldConfFilePath := filepath.Join(ldConfDirPath, customCDILinkerConfFile)

		// Try to open existing file for reading and appending.
		ldConfFile, err := cfs.OpenFile(ldConfFilePath, os.O_APPEND|os.O_RDWR)
		if err == nil {
			defer ldConfFile.Close()

			// The file already exists. Read it first, analyze its entries
			// and add the ones that are not already there.
			existingLinkerEntries := make(map[string]bool)
			scanner := bufio.NewScanner(ldConfFile)
			for scanner.Scan() {
				existingLinkerEntries[strings.TrimSpace(scanner.Text())] = true
			}

			if scanner.Err() != nil {
				return fmt.Errorf("Failed reading the linker conf file at %q: %w", ldConfFilePath, scanner.Err())
			}

			for _, update := range hooks.LDCacheUpdates {
				if !existingLinkerEntries[update] {
					_, err = fmt.Fprintln(ldConfFile, update)
					if err != nil {
						return fmt.Errorf("Failed writing to the linker conf file at %q: %w", ldConfFilePath, err)
					}

					existingLinkerEntries[update] = true
				}
			}
		} else {
			// The file does not exist. Create it with our entries.
			ldConfFile, err := cfs.OpenFile(ldConfFilePath, os.O_CREATE|os.O_WRONLY)
			if err != nil {
				return fmt.Errorf("Failed creating the linker conf file at %q: %w", ldConfFilePath, err)
			}

			defer ldConfFile.Close()

			for _, update := range hooks.LDCacheUpdates {
				_, err = fmt.Fprintln(ldConfFile, update)
				if err != nil {
					return fmt.Errorf("Failed writing to the linker conf file at %q: %w", ldConfFilePath, err)
				}
			}
		}
	}

	return nil
}

// UpdateLDCache updates the linker cache inside the instance. It ignores
// possible errors and logs them instead since this is a best effort action and
// failure should not impact the container's start or hotplugging.
func UpdateLDCache(inst instance.Instance) {
	l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

	if inst.IsRunning() {
		// Run ldconfig to update the linker cache, note we do not update symlinks via
		// -X as those are handled by the CDI hooks.
		cmd, err := inst.Exec(api.InstanceExecPost{
			Command:   []string{"/sbin/ldconfig", "-X"},
			WaitForWS: false,
		}, nil, nil, nil)

		if err != nil {
			l.Warn("Failed starting ldconfig in the container", logger.Ctx{"error": err})
			return
		}

		p, err := cmd.Wait()
		if err != nil {
			l.Warn("Failed executing ldconfig in the container", logger.Ctx{"error": err, "exit code": p})
		}
	} else {
		// For stopped containers, add touch /usr mtime. This triggers systemd's
		// ldconfig.service at boot to pick up the CDI libraries.
		// See systemctl cat ldconfig.service for details.
		err := os.Chtimes(filepath.Join(inst.RootfsPath(), "usr"), time.Now(), time.Now())
		if err != nil {
			l.Warn("Failed updating mtime of /usr in the container to trigger ldconfig.service", logger.Ctx{"error": err})
		}
	}
}
