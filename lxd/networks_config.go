package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lxc/lxd/shared"
)

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

var networkConfigKeys = map[string]func(value string) error{
	"bridge.driver": func(value string) error {
		return shared.IsOneOf(value, []string{"native", "openvswitch"})
	},
	"bridge.external_interfaces": func(value string) error {
		if value == "" {
			return nil
		}

		for _, entry := range strings.Split(value, ",") {
			entry = strings.TrimSpace(entry)
			if networkValidName(entry) != nil {
				return fmt.Errorf("Invalid interface name '%s'", entry)
			}
		}

		return nil
	},
	"bridge.mtu": shared.IsInt64,
	"bridge.mode": func(value string) error {
		return shared.IsOneOf(value, []string{"standard", "fan"})
	},

	"fan.overlay_subnet": networkValidNetworkV4,
	"fan.underlay_subnet": func(value string) error {
		if value == "auto" {
			return nil
		}

		return networkValidNetworkV4(value)
	},

	"tunnel.TARGET.protocol": func(value string) error {
		return shared.IsOneOf(value, []string{"gre", "vxlan"})
	},
	"tunnel.TARGET.local":  networkValidAddress,
	"tunnel.TARGET.remote": networkValidAddress,
	"tunnel.TARGET.port":   networkValidPort,
	"tunnel.TARGET.id":     shared.IsInt64,

	"ipv4.address": func(value string) error {
		if shared.IsOneOf(value, []string{"none", "auto"}) == nil {
			return nil
		}

		return networkValidAddressV4(value)
	},
	"ipv4.nat":         shared.IsBool,
	"ipv4.dhcp":        shared.IsBool,
	"ipv4.dhcp.ranges": shared.IsAny,
	"ipv4.routing":     shared.IsBool,

	"ipv6.address": func(value string) error {
		if shared.IsOneOf(value, []string{"none", "auto"}) == nil {
			return nil
		}

		return networkValidAddressV6(value)
	},
	"ipv6.nat":           shared.IsBool,
	"ipv6.dhcp":          shared.IsBool,
	"ipv6.dhcp.stateful": shared.IsBool,
	"ipv6.dhcp.ranges":   shared.IsAny,
	"ipv6.routing":       shared.IsBool,

	"dns.domain": shared.IsAny,
	"dns.mode": func(value string) error {
		return shared.IsOneOf(value, []string{"dynamic", "managed", "none"})
	},

	"raw.dnsmasq": shared.IsAny,
}

func networkValidateConfig(config map[string]string) error {
	bridgeMode := config["bridge.mode"]

	for k, v := range config {
		key := k

		// User keys are free for all
		if strings.HasPrefix(key, "user.") {
			continue
		}

		// Tunnel keys have the remote name in their name, so extract the real key
		if strings.HasPrefix(key, "tunnel.") {
			fields := strings.Split(key, ".")
			if len(fields) != 3 {
				return fmt.Errorf("Invalid network configuration key: %s", k)
			}

			key = fmt.Sprintf("tunnel.TARGET.%s", fields[2])
		}

		// Then validate
		validator, ok := networkConfigKeys[key]
		if !ok {
			return fmt.Errorf("Invalid network configuration key: %s", k)
		}

		err := validator(v)
		if err != nil {
			return err
		}

		// Bridge mode checks
		if bridgeMode == "fan" && strings.HasPrefix(key, "ipv4.") && v != "" {
			return fmt.Errorf("IPv4 configuration may not be set when in 'fan' mode")
		}

		if bridgeMode == "fan" && strings.HasPrefix(key, "ipv6.") && v != "" {
			return fmt.Errorf("IPv6 configuration may not be set when in 'fan' mode")
		}

		if bridgeMode != "fan" && strings.HasPrefix(key, "fan.") && v != "" {
			return fmt.Errorf("FAN configuration may only be set when in 'fan' mode")
		}

		// MTU checks
		if key == "bridge.mtu" && v != "" {
			mtu, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("Invalid value for an integer: %s", v)
			}

			ipv6 := config["ipv6.address"]
			if ipv6 != "" && ipv6 != "none" && mtu < 1280 {
				return fmt.Errorf("The minimum MTU for an IPv6 network is 1280")
			}

			ipv4 := config["ipv4.address"]
			if ipv4 != "" && ipv4 != "none" && mtu < 68 {
				return fmt.Errorf("The minimum MTU for an IPv4 network is 68")
			}

			if config["bridge.mode"] == "fan" && mtu > 1450 {
				return fmt.Errorf("Maximum MTU for a FAN bridge is 1450")
			}
		}
	}

	return nil
}
