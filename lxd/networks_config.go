package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lxc/lxd/shared"
)

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
	"bridge.hwaddr": shared.IsAny,
	"bridge.mtu":    shared.IsInt64,
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
	"fan.type": func(value string) error {
		return shared.IsOneOf(value, []string{"vxlan", "ipip"})
	},

	"tunnel.TARGET.protocol": func(value string) error {
		return shared.IsOneOf(value, []string{"gre", "vxlan"})
	},
	"tunnel.TARGET.local":     networkValidAddressV4,
	"tunnel.TARGET.remote":    networkValidAddressV4,
	"tunnel.TARGET.port":      networkValidPort,
	"tunnel.TARGET.group":     networkValidAddressV4,
	"tunnel.TARGET.id":        shared.IsInt64,
	"tunnel.TARGET.interface": networkValidName,
	"tunnel.TARGET.ttl":       shared.IsUint8,

	"ipv4.address": func(value string) error {
		if shared.IsOneOf(value, []string{"none", "auto"}) == nil {
			return nil
		}

		return networkValidAddressCIDRV4(value)
	},
	"ipv4.firewall": shared.IsBool,
	"ipv4.nat":      shared.IsBool,
	"ipv4.nat.order": func(value string) error {
		return shared.IsOneOf(value, []string{"before", "after"})
	},
	"ipv4.dhcp":         shared.IsBool,
	"ipv4.dhcp.gateway": networkValidAddressV4,
	"ipv4.dhcp.expiry":  shared.IsAny,
	"ipv4.dhcp.ranges":  shared.IsAny,
	"ipv4.routes":       shared.IsAny,
	"ipv4.routing":      shared.IsBool,

	"ipv6.address": func(value string) error {
		if shared.IsOneOf(value, []string{"none", "auto"}) == nil {
			return nil
		}

		return networkValidAddressCIDRV6(value)
	},
	"ipv6.firewall": shared.IsBool,
	"ipv6.nat":      shared.IsBool,
	"ipv6.nat.order": func(value string) error {
		return shared.IsOneOf(value, []string{"before", "after"})
	},
	"ipv6.dhcp":          shared.IsBool,
	"ipv6.dhcp.expiry":   shared.IsAny,
	"ipv6.dhcp.stateful": shared.IsBool,
	"ipv6.dhcp.ranges":   shared.IsAny,
	"ipv6.routes":        shared.IsAny,
	"ipv6.routing":       shared.IsBool,

	"dns.domain": shared.IsAny,
	"dns.mode": func(value string) error {
		return shared.IsOneOf(value, []string{"dynamic", "managed", "none"})
	},

	"raw.dnsmasq": shared.IsAny,
}

func networkValidateConfig(name string, config map[string]string) error {
	bridgeMode := config["bridge.mode"]

	if bridgeMode == "fan" && len(name) > 11 {
		return fmt.Errorf("Network name too long to use with the FAN (must be 11 characters or less)")
	}

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

			if len(name)+len(fields[1]) > 14 {
				return fmt.Errorf("Network name too long for tunnel interface: %s-%s", name, fields[1])
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

			if config["bridge.mode"] == "fan" {
				if config["fan.type"] == "ipip" {
					if mtu > 1480 {
						return fmt.Errorf("Maximum MTU for an IPIP FAN bridge is 1480")
					}
				} else {
					if mtu > 1450 {
						return fmt.Errorf("Maximum MTU for a VXLAN FAN bridge is 1450")
					}
				}
			}
		}
	}

	return nil
}

func networkFillAuto(config map[string]string) error {
	if config["ipv4.address"] == "auto" {
		subnet, err := networkRandomSubnetV4()
		if err != nil {
			return err
		}

		config["ipv4.address"] = subnet
	}

	if config["ipv6.address"] == "auto" {
		subnet, err := networkRandomSubnetV6()
		if err != nil {
			return err
		}

		config["ipv6.address"] = subnet
	}

	if config["fan.underlay_subnet"] == "auto" {
		subnet, _, err := networkDefaultGatewaySubnetV4()
		if err != nil {
			return err
		}

		config["fan.underlay_subnet"] = subnet.String()
	}

	return nil
}
