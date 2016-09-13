package main

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
)

func networkValidName(value string) error {
	// Validate the length
	if len(value) < 2 {
		return fmt.Errorf("Interface name is too short (minimum 2 characters)")
	}

	if len(value) > 15 {
		return fmt.Errorf("Interface name is too long (maximum 15 characters)")
	}

	// Validate the character set
	match, _ := regexp.MatchString("^[-a-zA-Z0-9]*$", value)
	if !match {
		return fmt.Errorf("Interface name contains invalid characters")
	}

	return nil
}

func networkValidPort(value string) error {
	if value == "" {
		return nil
	}

	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("Invalid value for an integer: %s", value)
	}

	if valueInt < 1 || valueInt > 65536 {
		return fmt.Errorf("Invalid port number: %s", value)
	}

	return nil
}

func networkValidAddress(value string) error {
	err := networkValidAddressV4(value)
	if err == nil {
		return nil
	}

	err = networkValidAddressV6(value)
	if err == nil {
		return nil
	}

	return fmt.Errorf("Not a valid address: %s", value)
}

func networkValidAddressV6(value string) error {
	if value == "" {
		return nil
	}

	ip, net, err := net.ParseCIDR(value)
	if err != nil {
		return err
	}

	if ip.To4() != nil {
		return fmt.Errorf("Not an IPv6 address: %s", value)
	}

	if ip.String() == net.IP.String() {
		return fmt.Errorf("Not a usable IPv6 address: %s", value)
	}

	return nil
}

func networkValidNetworkV6(value string) error {
	if value == "" {
		return nil
	}

	ip, net, err := net.ParseCIDR(value)
	if err != nil {
		return err
	}

	if ip.To4() != nil {
		return fmt.Errorf("Not an IPv6 network: %s", value)
	}

	if ip.String() != net.IP.String() {
		return fmt.Errorf("Not an IPv6 network address: %s", value)
	}

	return nil
}

func networkValidAddressV4(value string) error {
	if value == "" {
		return nil
	}

	ip, net, err := net.ParseCIDR(value)
	if err != nil {
		return err
	}

	if ip.To4() == nil {
		return fmt.Errorf("Not an IPv4 address: %s", value)
	}

	if ip.String() == net.IP.String() {
		return fmt.Errorf("Not a usable IPv4 address: %s", value)
	}

	return nil
}

func networkValidNetworkV4(value string) error {
	if value == "" {
		return nil
	}

	ip, net, err := net.ParseCIDR(value)
	if err != nil {
		return err
	}

	if ip.To4() == nil {
		return fmt.Errorf("Not an IPv4 network: %s", value)
	}

	if ip.String() != net.IP.String() {
		return fmt.Errorf("Not an IPv4 network address: %s", value)
	}

	return nil
}
