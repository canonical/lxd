package validate

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/lxc/lxd/shared/units"
)

// stringInSlice checks whether the supplied string is present in the supplied slice.
func stringInSlice(key string, list []string) bool {
	for _, entry := range list {
		if entry == key {
			return true
		}
	}
	return false
}

// Required returns function that runs one or more validators, all must pass without error.
func Required(validators ...func(value string) error) func(value string) error {
	return func(value string) error {
		for _, validator := range validators {
			err := validator(value)
			if err != nil {
				return err
			}
		}

		return nil
	}
}

// Optional wraps Required() function to make it return nil if value is empty string.
func Optional(validators ...func(value string) error) func(value string) error {
	return func(value string) error {
		if value == "" {
			return nil
		}

		return Required(validators...)(value)
	}
}

// IsInt64 validates whether the string can be converted to an int64.
func IsInt64(value string) error {
	_, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("Invalid value for an integer %q", value)
	}

	return nil
}

// IsUint8 validates whether the string can be converted to an uint8.
func IsUint8(value string) error {
	_, err := strconv.ParseUint(value, 10, 8)
	if err != nil {
		return fmt.Errorf("Invalid value for an integer %q. Must be between 0 and 255", value)
	}

	return nil
}

// IsUint32 validates whether the string can be converted to an uint32.
func IsUint32(value string) error {
	_, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return fmt.Errorf("Invalid value for uint32 %q: %v", value, err)
	}

	return nil
}

// IsPriority validates priority number.
func IsPriority(value string) error {
	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("Invalid value for an integer %q", value)
	}

	if valueInt < 0 || valueInt > 10 {
		return fmt.Errorf("Invalid value for a limit %q. Must be between 0 and 10", value)
	}

	return nil
}

// IsBool validates if string can be understood as a bool.
func IsBool(value string) error {
	if !stringInSlice(strings.ToLower(value), []string{"true", "false", "yes", "no", "1", "0", "on", "off"}) {
		return fmt.Errorf("Invalid value for a boolean %q", value)
	}

	return nil
}

// IsOneOf checks whether the string is present in the supplied slice of strings.
func IsOneOf(value string, valid []string) error {
	if value == "" {
		return nil
	}

	if !stringInSlice(value, valid) {
		return fmt.Errorf("Invalid value %q (not one of %s)", value, valid)
	}

	return nil
}

// IsAny accepts all strings as valid.
func IsAny(value string) error {
	return nil
}

// IsNotEmpty requires a non-empty string.
func IsNotEmpty(value string) error {
	if value == "" {
		return fmt.Errorf("Required value")
	}

	return nil
}

// IsSize checks if string is valid size according to units.ParseByteSizeString.
func IsSize(value string) error {
	_, err := units.ParseByteSizeString(value)
	if err != nil {
		return err
	}

	return nil
}

// IsDeviceID validates string is four lowercase hex characters suitable as Vendor or Device ID.
func IsDeviceID(value string) error {
	regexHexLc, err := regexp.Compile("^[0-9a-f]+$")
	if err != nil {
		return err
	}

	if len(value) != 4 || !regexHexLc.MatchString(value) {
		return fmt.Errorf("Invalid value, must be four lower case hex characters")
	}

	return nil
}

// IsNetworkMAC validates an Ethernet MAC address. e.g. "00:00:5e:00:53:01".
func IsNetworkMAC(value string) error {
	_, err := net.ParseMAC(value)

	// Check is valid Ethernet MAC length and delimiter.
	if err != nil || len(value) != 17 || strings.ContainsAny(value, "-.") {
		return fmt.Errorf("Invalid MAC address, must be 6 bytes of hex separated by colons")
	}

	return nil
}

// IsNetworkAddress validates an IP (v4 or v6) address string. If string is empty, returns valid.
func IsNetworkAddress(value string) error {
	ip := net.ParseIP(value)
	if ip == nil {
		return fmt.Errorf("Not an IP address %q", value)
	}

	return nil
}

// IsNetworkV4 validates an IPv4 CIDR string. If string is empty, returns valid.
func IsNetworkV4(value string) error {
	ip, subnet, err := net.ParseCIDR(value)
	if err != nil {
		return err
	}

	if ip.To4() == nil {
		return fmt.Errorf("Not an IPv4 network %q", value)
	}

	if ip.String() != subnet.IP.String() {
		return fmt.Errorf("Not an IPv4 network address %q", value)
	}

	return nil
}

// IsNetworkAddressV4 validates an IPv4 addresss string. If string is empty, returns valid.
func IsNetworkAddressV4(value string) error {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("Not an IPv4 address %q", value)
	}

	return nil
}

// IsNetworkAddressCIDRV4 validates an IPv4 addresss string in CIDR format. If string is empty, returns valid.
func IsNetworkAddressCIDRV4(value string) error {
	ip, subnet, err := net.ParseCIDR(value)
	if err != nil {
		return err
	}

	if ip.To4() == nil {
		return fmt.Errorf("Not an IPv4 address %q", value)
	}

	if ip.String() == subnet.IP.String() {
		return fmt.Errorf("Not a usable IPv4 address %q", value)
	}

	return nil
}

// IsNetworkAddressV4List validates a comma delimited list of IPv4 addresses.
func IsNetworkAddressV4List(value string) error {
	for _, v := range strings.Split(value, ",") {
		v = strings.TrimSpace(v)
		err := IsNetworkAddressV4(v)
		if err != nil {
			return err
		}
	}
	return nil
}

// IsNetworkV4List validates a comma delimited list of IPv4 CIDR strings.
func IsNetworkV4List(value string) error {
	for _, network := range strings.Split(value, ",") {
		network = strings.TrimSpace(network)
		err := IsNetworkV4(network)
		if err != nil {
			return err
		}
	}

	return nil
}

// IsNetworkV6 validates an IPv6 CIDR string. If string is empty, returns valid.
func IsNetworkV6(value string) error {
	ip, subnet, err := net.ParseCIDR(value)
	if err != nil {
		return err
	}

	if ip == nil || ip.To4() != nil {
		return fmt.Errorf("Not an IPv6 network %q", value)
	}

	if ip.String() != subnet.IP.String() {
		return fmt.Errorf("Not an IPv6 network address %q", value)
	}

	return nil
}

// IsNetworkAddressV6 validates an IPv6 addresss string. If string is empty, returns valid.
func IsNetworkAddressV6(value string) error {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() != nil {
		return fmt.Errorf("Not an IPv6 address %q", value)
	}

	return nil
}

// IsNetworkAddressCIDRV6 validates an IPv6 addresss string in CIDR format. If string is empty, returns valid.
func IsNetworkAddressCIDRV6(value string) error {
	ip, subnet, err := net.ParseCIDR(value)
	if err != nil {
		return err
	}

	if ip.To4() != nil {
		return fmt.Errorf("Not an IPv6 address %q", value)
	}

	if ip.String() == subnet.IP.String() {
		return fmt.Errorf("Not a usable IPv6 address %q", value)
	}

	return nil
}

// IsNetworkAddressV6List validates a comma delimited list of IPv6 addresses.
func IsNetworkAddressV6List(value string) error {
	for _, v := range strings.Split(value, ",") {
		v = strings.TrimSpace(v)
		err := IsNetworkAddressV6(v)
		if err != nil {
			return err
		}
	}
	return nil
}

// IsNetworkV6List validates a comma delimited list of IPv6 CIDR strings.
func IsNetworkV6List(value string) error {
	for _, network := range strings.Split(value, ",") {
		network = strings.TrimSpace(network)
		err := IsNetworkV6(network)
		if err != nil {
			return err
		}
	}

	return nil
}

// IsNetworkRangeV4 validates an IPv4 range in the format "start-end".
func IsNetworkRangeV4(value string) error {
	ips := strings.SplitN(value, "-", 2)
	if len(ips) != 2 {
		return fmt.Errorf("IP range must contain start and end IP addresses")
	}

	for _, ip := range ips {
		err := IsNetworkAddressV4(ip)
		if err != nil {
			return err
		}
	}

	return nil
}

// IsNetworkRangeV4List validates a comma delimited list of IPv4 ranges.
func IsNetworkRangeV4List(value string) error {
	for _, ipRange := range strings.Split(value, ",") {
		err := IsNetworkRangeV4(strings.TrimSpace(ipRange))
		if err != nil {
			return err
		}
	}

	return nil
}

// IsNetworkRangeV6 validates an IPv6 range in the format "start-end".
func IsNetworkRangeV6(value string) error {
	ips := strings.SplitN(value, "-", 2)
	if len(ips) != 2 {
		return fmt.Errorf("IP range must contain start and end IP addresses")
	}

	for _, ip := range ips {
		err := IsNetworkAddressV6(ip)
		if err != nil {
			return err
		}
	}

	return nil
}

// IsNetworkRangeV6List validates a comma delimited list of IPv6 ranges.
func IsNetworkRangeV6List(value string) error {
	for _, ipRange := range strings.Split(value, ",") {
		err := IsNetworkRangeV6(strings.TrimSpace(ipRange))
		if err != nil {
			return err
		}
	}

	return nil
}

// IsNetworkVLAN validates a VLAN ID.
func IsNetworkVLAN(value string) error {
	vlanID, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("Invalid VLAN ID %q", value)
	}

	if vlanID < 0 || vlanID > 4094 {
		return fmt.Errorf("Out of VLAN ID range (0-4094) %q", value)
	}

	return nil
}

// IsNetworkMTU validates MTU number >= 1280 and <= 9202.
// Anything below 68 and the kernel doesn't allow IPv4, anything below 1280 and the kernel doesn't allow IPv6.
// So require an IPv6-compatible MTU as the low value and cap at the max ethernet jumbo frame size.
func IsNetworkMTU(value string) error {
	mtu, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return fmt.Errorf("Invalid MTU %q", value)
	}

	if mtu < 1280 || mtu > 9202 {
		return fmt.Errorf("Out of MTU range (1280-9202) %q", value)
	}

	return nil
}

// IsURLSegmentSafe validates whether value can be used in a URL segment.
func IsURLSegmentSafe(value string) error {
	for _, char := range []string{"/", "?", "&", "+"} {
		if strings.Contains(value, char) {
			return fmt.Errorf("Cannot contain %q", char)
		}
	}

	return nil
}
