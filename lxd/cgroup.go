package main

import (
	"bufio"
	"io/ioutil"
	"os"
	"path"
	"strings"
)

func getInitCgroupPath(controller string) string {
	f, err := os.Open("/proc/1/cgroup")
	if err != nil {
		return "/"
	}
	defer f.Close()

	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()

		fields := strings.Split(line, ":")
		if len(fields) != 3 {
			return "/"
		}

		if fields[2] != controller {
			continue
		}

		initPath := string(fields[3])

		// ignore trailing /init.scope if it is there
		dir, file := path.Split(initPath)
		if file == "init.scope" {
			return dir
		} else {
			return initPath
		}
	}

	return "/"
}

func cGroupGet(controller, cgroup, file string) (string, error) {
	initPath := getInitCgroupPath(controller)
	path := path.Join("/sys/fs/cgroup", controller, initPath, cgroup, file)

	contents, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.Trim(string(contents), "\n"), nil
}

func cGroupSet(controller, cgroup, file string, value string) error {
	initPath := getInitCgroupPath(controller)
	path := path.Join("/sys/fs/cgroup", controller, initPath, cgroup, file)

	return ioutil.WriteFile(path, []byte(value), 0755)
}
