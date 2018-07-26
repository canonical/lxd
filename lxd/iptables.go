package main

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/lxc/lxd/shared"
)

func iptablesConfig(protocol string, comment string, table string, method string, chain string,
	rule ...string) error {
	cmd := "iptables"
	if protocol == "ipv6" {
		cmd = "ip6tables"
	}

	_, err := exec.LookPath(cmd)
	if err != nil {
		return fmt.Errorf("Asked to setup %s firewalling but %s can't be found", protocol, cmd)
	}

	baseArgs := []string{"-w"}
	if table == "" {
		table = "filter"
	}
	baseArgs = append(baseArgs, []string{"-t", table}...)

	// Check for an existing entry
	args := append(baseArgs, []string{"-C", chain}...)
	args = append(args, rule...)
	args = append(args, "-m", "comment", "--comment", fmt.Sprintf("generated for %s", comment))
	_, err = shared.RunCommand(cmd, args...)
	if err == nil {
		return nil
	}

	args = append(baseArgs, []string{method, chain}...)
	args = append(args, rule...)
	args = append(args, "-m", "comment", "--comment", fmt.Sprintf("generated for %s", comment))

	_, err = shared.TryRunCommand(cmd, args...)
	if err != nil {
		return err
	}

	return nil
}

func iptablesAppend(protocol string, comment string, table string, chain string, rule ...string) error {
	return iptablesConfig(protocol, comment, table, "-A", chain, rule...)
}

func iptablesPrepend(protocol string, comment string, table string, chain string, rule ...string) error {
	return iptablesConfig(protocol, comment, table, "-I", chain, rule...)
}

func iptablesClear(protocol string, comment string, table string) error {
	// Detect kernels that lack IPv6 support
	if !shared.PathExists("/proc/sys/net/ipv6") && protocol == "ipv6" {
		return nil
	}

	cmd := "iptables"
	if protocol == "ipv6" {
		cmd = "ip6tables"
	}

	_, err := exec.LookPath(cmd)
	if err != nil {
		return nil
	}

	baseArgs := []string{"-w"}
	if table == "" {
		table = "filter"
	}
	baseArgs = append(baseArgs, []string{"-t", table}...)

	// List the rules
	args := append(baseArgs, "-S")
	output, err := shared.TryRunCommand(cmd, args...)
	if err != nil {
		return fmt.Errorf("Failed to list %s rules for %s (table %s)", protocol, comment, table)
	}

	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, fmt.Sprintf("generated for %s", comment)) {
			continue
		}

		// Remove the entry
		fields := strings.Fields(line)
		fields[0] = "-D"

		args = append(baseArgs, fields...)
		_, err = shared.TryRunCommand("sh", "-c", fmt.Sprintf("%s %s", cmd, strings.Join(args, " ")))
		if err != nil {
			return err
		}
	}

	return nil
}

func networkIptablesAppend(protocol string, comment string, table string, chain string,
	rule ...string) error {
	return iptablesAppend(protocol, fmt.Sprintf("LXD network %s", comment),
		table, chain, rule...)
}

func networkIptablesPrepend(protocol string, comment string, table string, chain string,
	rule ...string) error {
	return iptablesPrepend(protocol, fmt.Sprintf("LXD network %s", comment),
		table, chain, rule...)
}

func networkIptablesClear(protocol string, comment string, table string) error {
	return iptablesClear(protocol, fmt.Sprintf("LXD network %s", comment),
		table)
}

func containerIptablesPrepend(protocol string, comment string, table string,
	chain string, rule ...string) error {
	return iptablesPrepend(protocol, fmt.Sprintf("LXD container %s", comment),
		table, chain, rule...)
}

func containerIptablesClear(protocol string, comment string, table string) error {
	return iptablesClear(protocol, fmt.Sprintf("LXD container %s", comment),
		table)
}
