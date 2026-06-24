package main

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

type lxdDaemon struct {
	s    lxd.ContainerServer
	path string

	info         *api.Server
	containers   []api.Container
	images       []api.Image
	networks     []api.Network
	storagePools []api.StoragePool
}

func lxdConnect(path string) (*lxdDaemon, error) {
	// Connect to the LXD daemon
	s, err := lxd.ConnectLXDUnix(fmt.Sprintf("%s/unix.socket", path), nil)
	if err != nil {
		return nil, err
	}

	// Setup our internal struct
	d := &lxdDaemon{s: s, path: path}

	// Get a bunch of data from the daemon
	err = d.update()
	if err != nil {
		return nil, err
	}

	return d, nil
}

func (d *lxdDaemon) update() error {
	// Daemon
	info, _, err := d.s.GetServer()
	if err != nil {
		return err
	}

	d.info = info

	// Containers
	containers, err := d.s.GetContainers()
	if err != nil {
		return err
	}

	d.containers = containers

	// Images
	images, err := d.s.GetImages()
	if err != nil {
		return err
	}

	d.images = images

	// Networks
	if d.s.HasExtension("network") {
		networks, err := d.s.GetNetworks()
		if err != nil {
			return err
		}

		// We only care about the managed ones
		d.networks = []api.Network{}
		for _, network := range networks {
			if network.Managed {
				d.networks = append(d.networks, network)
			}
		}
	}

	// Storage pools
	if d.s.HasExtension("storage") {
		pools, err := d.s.GetStoragePools()
		if err != nil {
			return err
		}

		d.storagePools = pools
	}

	return nil
}

func (d *lxdDaemon) checkEmpty() error {
	// Containers
	if len(d.containers) > 0 {
		return fmt.Errorf("Target LXD already has containers, aborting.")
	}

	// Images
	if len(d.images) > 0 {
		return fmt.Errorf("Target LXD already has images, aborting.")
	}

	// Networks
	if d.networks != nil {
		if len(d.networks) > 0 {
			return fmt.Errorf("Target LXD already has networks, aborting.")
		}
	}

	// Storage pools
	if d.storagePools != nil {
		if len(d.storagePools) > 0 {
			return fmt.Errorf("Target LXD already has storage pools, aborting.")
		}
	}

	return nil
}

func (d *lxdDaemon) showReport() error {
	// Print a basic report to the console
	fmt.Printf("LXD version: %s\n", d.info.Environment.ServerVersion)
	fmt.Printf("LXD PID: %d\n", d.info.Environment.ServerPid)
	fmt.Printf("Resources:\n")
	fmt.Printf("  Containers: %d\n", len(d.containers))
	fmt.Printf("  Images: %d\n", len(d.images))
	if d.networks != nil {
		fmt.Printf("  Networks: %d\n", len(d.networks))
	}
	if d.storagePools != nil {
		fmt.Printf("  Storage pools: %d\n", len(d.storagePools))
	}

	return nil
}

func (d *lxdDaemon) shutdown() error {
	// Send the shutdown request
	_, _, err := d.s.RawQuery("PUT", "/internal/shutdown", nil, "")
	if err != nil {
		return err
	}

	// Wait for the daemon to exit
	chMonitor := make(chan bool, 1)
	go func() {
		monitor, err := d.s.GetEvents()
		if err != nil {
			close(chMonitor)
			return
		}

		monitor.Wait()
		close(chMonitor)
	}()

	// Wait for the daemon to exit or timeout to be reached
	select {
	case <-chMonitor:
		break
	case <-time.After(time.Second * time.Duration(300)):
		return fmt.Errorf("LXD still running after 5 minutes")
	}

	return nil
}

func (d *lxdDaemon) wait(clustered bool) error {
	finger := make(chan error, 1)
	go func() {
		for {
			c, err := lxd.ConnectLXDUnix(filepath.Join(d.path, "unix.socket"), nil)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			_, _, err = c.RawQuery("GET", "/internal/ready", nil, "")
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			finger <- nil
			return
		}
	}()

	timeout := time.Second * time.Duration(300)
	if clustered {
		timeout = time.Hour
	}

	select {
	case <-finger:
		break
	case <-time.After(time.Second * timeout):
		return fmt.Errorf("LXD still not running after 5 minutes.")
	}

	return nil
}

func (d *lxdDaemon) reload() error {
	// Reload or restart the relevant systemd units
	if strings.HasPrefix(d.path, "/var/snap") {
		return systemdCtl("reload", "snap.lxd.daemon.service")
	}

	if osInit() == "upstart" {
		return upstartCtl("restart", "lxd")
	}

	return systemdCtl("restart", "lxd.service", "lxd.socket")
}

func (d *lxdDaemon) start() error {
	// Start the relevant systemd units
	if strings.HasPrefix(d.path, "/var/snap") {
		return systemdCtl("start", "snap.lxd.daemon.service")
	}

	if osInit() == "upstart" {
		return upstartCtl("start", "lxd")
	}

	return systemdCtl("start", "lxd.service", "lxd.socket")
}

func (d *lxdDaemon) stop() error {
	// Stop the relevant systemd units
	if strings.HasPrefix(d.path, "/var/snap") {
		if systemdCtl("is-active", "snap.lxd.daemon.unix.socket") == nil {
			return systemdCtl("stop", "snap.lxd.daemon.unix.socket", "snap.lxd.daemon.service")
		}

		return systemdCtl("stop", "snap.lxd.daemon.service")
	}

	if osInit() == "upstart" {
		return upstartCtl("stop", "lxd")
	}

	return systemdCtl("stop", "lxd.service", "lxd.socket")
}

func (d *lxdDaemon) uninstall() error {
	// Remove the LXD package
	if strings.HasPrefix(d.path, "/var/snap") {
		_, err := shared.RunCommand("snap", "remove", "lxd")
		return err
	}

	// Actively kill the old systemd units in case they get stuck during removal
	chDone := make(chan struct{}, 1)
	defer close(chDone)

	go func() {
		for {
			select {
			case <-chDone:
				return
			case <-time.After(10 * time.Second):
				shared.RunCommand("systemctl", "kill", "-s", "SIGKILL", "lxd-containers.service")
				shared.RunCommand("systemctl", "kill", "-s", "SIGKILL", "lxd.service")
			}
		}
	}()

	// Remove LXD itself
	_, err := shared.RunCommand("apt-get", "remove", "--purge", "--yes", "lxd", "lxd-client")
	if err != nil {
		return err
	}

	// Check if we can get rid of liblxc1, liblxc-common and lxcfs too
	//// Ubuntu 18.04
	err = packagesRemovable([]string{"liblxc1", "liblxc-common", "lxcfs"})
	if err == nil {
		_, err := shared.RunCommand("apt-get", "remove", "--purge", "--yes", "liblxc1", "liblxc-common", "lxcfs")
		if err != nil {
			return err
		}

		return nil
	}

	//// Ubuntu 16.04
	err = packagesRemovable([]string{"liblxc1", "lxc-common", "lxcfs"})
	if err == nil {
		_, err := shared.RunCommand("apt-get", "remove", "--purge", "--yes", "liblxc1", "lxc-common", "lxcfs")
		if err != nil {
			return err
		}

		return nil
	}

	return nil
}

func (d *lxdDaemon) wipe() error {
	// Check if the path is already gone
	if !shared.PathExists(d.path) {
		return nil
	}

	return os.RemoveAll(d.path)
}

func (d *lxdDaemon) moveFiles(dst string) error {
	// Create the logs directory if missing (needed by LXD)
	if !shared.PathExists(filepath.Join(d.path, "logs")) {
		err := os.MkdirAll(filepath.Join(d.path, "logs"), 0755)
		if err != nil {
			return err
		}
	}

	// Move the daemon path to a new target
	_, err := shared.RunCommand("mv", d.path, dst)
	if err != nil {
		return err
	}

	return nil
}

func (d *lxdDaemon) cleanMounts() error {
	mounts := []string{}

	// Get all the mounts under the daemon path
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		s := strings.Split(scanner.Text(), " ")
		if strings.HasPrefix(s[4], d.path) {
			mounts = append(mounts, s[4])
		}
	}

	// Reverse the list
	sort.Sort(sort.Reverse(sort.StringSlice(mounts)))

	// Attempt to lazily unmount them all
	for _, mount := range mounts {
		if mount == d.path {
			continue
		}

		err = syscall.Unmount(mount, syscall.MNT_DETACH)
		if err != nil {
			return fmt.Errorf("Unable to unmount: %s: %v", mount, err)
		}
	}

	return nil
}

func (d *lxdDaemon) remount(dst string) error {
	// Create the logs directory if missing (needed by LXD)
	if !shared.PathExists(filepath.Join(d.path, "logs")) {
		err := os.MkdirAll(filepath.Join(d.path, "logs"), 0755)
		if err != nil {
			return err
		}
	}

	// Attempt a simple rename (for btrfs subvolumes)
	err := os.Rename(d.path, dst)
	if err == nil {
		return nil
	}

	// Create the target
	err = os.MkdirAll(dst, 0755)
	if err != nil {
		return err
	}

	// Bind-mount to the new target
	err = syscall.Mount(d.path, dst, "none", syscall.MS_BIND|syscall.MS_REC, "")
	if err != nil {
		return err
	}

	// Attempt to unmount the source path
	err = syscall.Unmount(d.path, syscall.MNT_DETACH)
	if err != nil {
		// If unmounting fails, then the source may have been a subvolume, in this case, just hide it
		err = syscall.Mount("tmpfs", d.path, "tmpfs", 0, "size=100k,mode=700")
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *lxdDaemon) rewriteStorage(dst *lxdDaemon) error {
	// Symlink rewrite function
	rewriteSymlink := func(path string) error {
		target, err := os.Readlink(path)
		if err != nil {
			// Not a symlink, skipping
			return nil
		}

		newTarget := convertPath(target, d.path, dst.path)
		if target != newTarget {
			err = os.Remove(path)
			if err != nil {
				return err
			}

			err = os.Symlink(newTarget, path)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// ZFS rewrite function
	zfsRewrite := func(zpool string) error {
		output, err := runSnapCommand("zfs", "list", "-H", "-t", "all", "-o", "name,mountpoint", "-r", zpool)
		if err != nil {
			// Print a clear error message but don't fail as that'd leave a broken LXD
			fmt.Println("")
			fmt.Printf("ERROR: Unable to access the '%s' ZPOOL at this time: %v\n", zpool, err)
			fmt.Printf("       Container mountpoints will need to be manually corrected.\n\n")
			return nil
		}

		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}

			name := fields[0]
			mountpoint := fields[1]

			if mountpoint == "none" || mountpoint == "-" {
				continue
			}

			mountpoint = convertPath(mountpoint, d.path, dst.path)
			_, err := runSnapCommand("zfs", "set", fmt.Sprintf("mountpoint=%s", mountpoint), name)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Rewrite the container links
	containers, err := ioutil.ReadDir(filepath.Join(dst.path, "containers"))
	if err != nil {
		return err
	}

	for _, ctn := range containers {
		err := rewriteSymlink(filepath.Join(dst.path, "containers", ctn.Name()))
		if err != nil {
			return err
		}
	}

	// Handle older LXD daemons
	if d.storagePools == nil {
		zpool, ok := d.info.Config["storage.zfs_pool_name"]
		if ok {
			err := zfsRewrite(zpool.(string))
			if err != nil {
				return err
			}
		}

		return nil
	}

	for _, pool := range d.storagePools {
		source := pool.Config["source"]
		newSource := convertPath(source, d.path, dst.path)
		if source != newSource {
			err := dbRewritePoolSource(d, dst, pool.Name, newSource)
			if err != nil {
				return err
			}

			pool.Config["source"] = newSource
		}

		if pool.Driver == "zfs" {
			// For ZFS we must rewrite all the mountpoints
			zpool := pool.Config["zfs.pool_name"]
			if zpool == "" {
				zpool = pool.Config["source"]
			}

			err = zfsRewrite(zpool)
			if err != nil {
				return err
			}

			continue
		}

		if pool.Driver == "dir" {
			// For dir we must rewrite any symlink
			err := rewriteSymlink(filepath.Join(dst.path, "storage-pools", pool.Name))
			if err != nil {
				return err
			}

			continue
		}
	}

	return nil
}

func (d *lxdDaemon) backupDatabase() error {
	for _, path := range []string{filepath.Join(d.path, "lxd.db"), filepath.Join(d.path, "raft"), filepath.Join(d.path, "database")} {
		if !shared.PathExists(path) {
			continue
		}

		backupPath := fmt.Sprintf("%s.pre-migration", path)
		if shared.PathExists(backupPath) {
			err := os.RemoveAll(backupPath)
			if err != nil {
				return err
			}
		}

		if shared.IsDir(path) {
			err := shared.DirCopy(path, backupPath)
			if err != nil {
				return err
			}
		} else {
			err := shared.FileCopy(path, backupPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
