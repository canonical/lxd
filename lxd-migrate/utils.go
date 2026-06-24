package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/canonical/lxd/shared"
)

func runSnapCommand(name string, arg ...string) (string, error) {
	args := []string{"--mount=/run/snapd/ns/lxd.mnt", name}
	args = append(args, arg...)

	return shared.RunCommand("nsenter", args...)
}

func compareVersions(a string, b string) int {
	aFields := strings.Split(a, ".")
	bFields := strings.Split(b, ".")

	fields := len(aFields)
	if len(bFields) > fields {
		fields = len(bFields)
	}

	// Iterate over parts of both versions
	for i := 0; i < fields; i++ {
		var err error

		// Parse the left part
		aInt := int64(0)
		if len(aFields) > i {
			aInt, err = strconv.ParseInt(aFields[i], 10, 64)
			if err != nil {
				aInt = 0
			}
		}

		// Parse the right part
		bInt := int64(0)
		if len(bFields) > i {
			bInt, err = strconv.ParseInt(bFields[i], 10, 64)
			if err != nil {
				bInt = 0
			}
		}

		// Compare versions
		if aInt == bInt {
			continue
		} else if aInt < bInt {
			return -1
		} else if aInt > bInt {
			return 1
		}
	}

	return 0
}

func systemdCtl(action string, units ...string) error {
	args := []string{}
	args = append(args, action)
	args = append(args, units...)

	// Run systemctl
	_, err := shared.RunCommand("systemctl", args...)
	return err
}

func upstartCtl(action string, units ...string) error {
	args := []string{}
	args = append(args, action)
	args = append(args, units...)

	// Run initctl
	_, err := shared.RunCommand("initctl", args...)
	return err
}

func convertPath(path string, src string, dst string) string {
	// Relative paths are handled by LXD
	if !strings.HasPrefix(path, "/") {
		return path
	}

	// /dev is always available
	if strings.HasPrefix(path, "/dev/") {
		return path
	}

	// Somehow the path is already correct
	if strings.HasPrefix(path, dst) {
		return path
	}

	// Prefixed with old path
	if strings.HasPrefix(path, src) {
		return fmt.Sprintf("%s%s", dst, strings.TrimPrefix(path, src))
	}

	// Requires host access
	return fmt.Sprintf("/var/lib/snapd/hostfs%s", path)
}

func osID() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return "unknown"
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		s := strings.Split(scanner.Text(), "=")
		if len(s) >= 2 && s[0] == "ID" {
			return s[1]
		}
	}

	return "unknown"
}

func osInit() string {
	initExe, err := os.Readlink("/proc/1/exe")
	if err != nil {
		return "unknown"
	}

	fields := strings.Split(initExe, " ")
	init := filepath.Base(fields[0])

	if init == "init" {
		init = "upstart"
	}

	return init
}

func packagesRemovable(names []string) error {
	rdepends := []string{}

	// Get all reverse depends
	output, err := shared.RunCommand("apt-cache", append([]string{"-i", "rdepends"}, names...)...)
	if err != nil {
		return err
	}

	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}

		pkg := strings.TrimSpace(line)
		if !shared.ValueInSlice(pkg, rdepends) && !shared.ValueInSlice(pkg, names) {
			rdepends = append(rdepends, pkg)
		}
	}

	// Check for what's installed
	problems := []string{}
	for _, pkg := range rdepends {
		output, err := shared.RunCommand("dpkg-query", "-W", "-f=${Status}", pkg)
		if err == nil && strings.HasSuffix(output, " installed") {
			if shared.ValueInSlice(pkg, []string{"adapt", "autopkgtest", "lxc2", "nova-compute-lxd", "snapcraft", "ubuntu-server"}) {
				continue
			}

			problems = append(problems, pkg)
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("The following packages depend on %v: %s", names, strings.Join(problems, ", "))
	}

	return nil
}
