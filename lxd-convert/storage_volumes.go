package main

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/client"
)

// parseStorageVolumeArg parses a "<device>,<key>=<value>,<key>=<value>..." flag value (the same
// syntax already used by "lxc init --device") into a device name and its config map.
func parseStorageVolumeArg(arg string) (string, map[string]string, error) {
	fields := strings.Split(arg, ",")
	if len(fields) < 2 {
		return "", nil, fmt.Errorf("Bad syntax, expecting <device>,<key>=<value>: %s", arg)
	}

	config := map[string]string{}
	for _, field := range fields[1:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return "", nil, fmt.Errorf("Bad syntax, expecting <key>=<value>: %s", field)
		}

		config[key] = value
	}

	return fields[0], config, nil
}

// tarDirectory streams the contents of dir as an uncompressed tar archive suitable for
// InstanceServer.CreateStoragePoolVolumeFromTarball.
func tarDirectory(dir string) io.ReadCloser {
	pr, pw := io.Pipe()

	go func() {
		tw := tar.NewWriter(pw)

		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			relPath, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}

			if relPath == "." {
				return nil
			}

			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, err = os.Readlink(path)
				if err != nil {
					return err
				}
			}

			hdr, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}

			hdr.Name = filepath.ToSlash(relPath)

			err = tw.WriteHeader(hdr)
			if err != nil {
				return err
			}

			if !info.Mode().IsRegular() {
				return nil
			}

			f, err := os.Open(path)
			if err != nil {
				return err
			}

			defer func() { _ = f.Close() }()

			_, err = io.Copy(tw, f)

			return err
		})

		if err != nil {
			_ = tw.Close()
			_ = pw.CloseWithError(err)

			return
		}

		_ = pw.CloseWithError(tw.Close())
	}()

	return pr
}

// applyStorageVolumeFlags configures additional custom storage volume devices requested through
// the --storage-volume and --storage-volume-from-path flags on the new instance.
func (c *cmdConvert) applyStorageVolumeFlags(server lxd.InstanceServer, config *cmdConvertData) error {
	for _, arg := range c.flagStorageVolumes {
		deviceName, devConfig, err := parseStorageVolumeArg(arg)
		if err != nil {
			return err
		}

		pool := devConfig["pool"]
		volName := devConfig["source"]
		path := devConfig["path"]

		if pool == "" || volName == "" {
			return fmt.Errorf(`Storage volume %q must set "pool" and "source": %s`, deviceName, arg)
		}

		if path == "" {
			return fmt.Errorf(`Storage volume %q must set "path": %s`, deviceName, arg)
		}

		vol, _, err := server.GetStoragePoolVolume(pool, "custom", volName)
		if err != nil {
			return fmt.Errorf("Failed getting storage volume %q on pool %q: %w", volName, pool, err)
		}

		if vol.ContentType != "filesystem" {
			return fmt.Errorf("Storage volume %q has content type %q: only filesystem volumes are supported by --storage-volume", volName, vol.ContentType)
		}

		config.InstanceArgs.Devices[deviceName] = map[string]string{
			"type":   "disk",
			"pool":   pool,
			"source": volName,
			"path":   path,
		}
	}

	for _, arg := range c.flagStorageVolumesFromPath {
		deviceName, devConfig, err := parseStorageVolumeArg(arg)
		if err != nil {
			return err
		}

		pool := devConfig["pool"]
		volName := devConfig["volume"]
		sourcePath := devConfig["source-path"]
		path := devConfig["path"]

		if pool == "" || volName == "" || sourcePath == "" {
			return fmt.Errorf(`Storage volume %q must set "pool", "volume" and "source-path": %s`, deviceName, arg)
		}

		if path == "" {
			return fmt.Errorf(`Storage volume %q must set "path": %s`, deviceName, arg)
		}

		info, err := os.Stat(sourcePath)
		if err != nil {
			return fmt.Errorf("Invalid source path %q: %w", sourcePath, err)
		}

		if !info.IsDir() {
			return fmt.Errorf("Invalid source path %q: not a directory", sourcePath)
		}

		tarData := tarDirectory(sourcePath)
		defer func() { _ = tarData.Close() }()

		op, err := server.CreateStoragePoolVolumeFromTarball(pool, lxd.StoragePoolVolumeBackupArgs{
			BackupFile: tarData,
			Name:       volName,
		})
		if err != nil {
			return fmt.Errorf("Failed creating storage volume %q on pool %q: %w", volName, pool, err)
		}

		err = op.Wait()
		if err != nil {
			return fmt.Errorf("Failed creating storage volume %q on pool %q: %w", volName, pool, err)
		}

		config.InstanceArgs.Devices[deviceName] = map[string]string{
			"type":   "disk",
			"pool":   pool,
			"source": volName,
			"path":   path,
		}
	}

	return nil
}
