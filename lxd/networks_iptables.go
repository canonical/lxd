package main

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/lxc/lxd/shared"
)

func networkIptablesPrepend(protocol string, netName string, table string, chain string, rule ...string) error {
	cmd := "iptables"
	if protocol == "ipv6" {
		cmd = "ip6tables"
	}

	baseArgs := []string{"-w"}
	if table != "" {
		baseArgs = append(baseArgs, []string{"-t", table}...)
	}

	// Check for an existing entry
	args := append(baseArgs, []string{"-C", chain}...)
	args = append(args, rule...)
	args = append(args, "-m", "comment", "--comment", fmt.Sprintf("generated for LXD network %s", netName))
	if shared.RunCommand(cmd, args...) == nil {
		return nil
	}

	// Add the rule
	args = append(baseArgs, []string{"-I", chain}...)
	args = append(args, rule...)
	args = append(args, "-m", "comment", "--comment", fmt.Sprintf("generated for LXD network %s", netName))

	err := shared.RunCommand(cmd, args...)
	if err != nil {
		return err
	}

	return nil
}

func networkIptablesClear(protocol string, netName string, table string) error {
	cmd := "iptables"
	if protocol == "ipv6" {
		cmd = "ip6tables"
	}

	baseArgs := []string{"-w"}
	if table != "" {
		baseArgs = append(baseArgs, []string{"-t", table}...)
	}

	// List the rules
	args := append(baseArgs, "-S")
	output, err := exec.Command(cmd, args...).Output()
	if err != nil {
		return fmt.Errorf("Failed to list %s rules for %s (table %s)", protocol, netName, table)
	}

	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, fmt.Sprintf("generated for LXD network %s", netName)) {
			continue
		}

		// Remove the entry
		fields := strings.Fields(line)
		fields[0] = "-D"

		args = append(baseArgs, fields...)
		err = shared.RunCommand("sh", "-c", fmt.Sprintf("%s %s", cmd, strings.Join(args, " ")))
		if err != nil {
			return err
		}
	}

	return nil
}
