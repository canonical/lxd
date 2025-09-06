package validate_test

import (
	"fmt"
	"testing"

	"github.com/canonical/lxd/shared/validate"
)

func ExampleIsNetworkMAC() {
	tests := []string{
		"00:00:5e:00:53:01",
		"02:00:5e:10:00:00:00:01", // too long
		"00-00-5e-00-53-01",       // invalid delimiter
		"0000.5e00.5301",          // invalid delimiter
		"invalid",
		"",
	}

	for _, v := range tests {
		err := validate.IsNetworkMAC(v)
		fmt.Printf("%s, %t\n", v, err == nil)
	}

	// Output: 00:00:5e:00:53:01, true
	// 02:00:5e:10:00:00:00:01, false
	// 00-00-5e-00-53-01, false
	// 0000.5e00.5301, false
	// invalid, false
	// , false
}

func ExampleIsPCIAddress() {
	tests := []string{
		"0000:12:ab.0", // valid
		"0010:12:ab.0", // valid
		"0000:12:CD.0", // valid
		"12:ab.0",      // valid
		"12:CD.0",      // valid
		"0000:12:gh.0", // invalid hex
		"0000:12:GH.0", // invalid hex
		"12:gh.0",      // invalid hex
		"12:GH.0",      // invalid hex
		"000:12:CD.0",  // wrong prefix
		"12.ab.0",      // invalid format
		"",
	}

	for _, v := range tests {
		err := validate.IsPCIAddress(v)
		fmt.Printf("%s, %t\n", v, err == nil)
	}

	// Output: 0000:12:ab.0, true
	// 0010:12:ab.0, true
	// 0000:12:CD.0, true
	// 12:ab.0, true
	// 12:CD.0, true
	// 0000:12:gh.0, false
	// 0000:12:GH.0, false
	// 12:gh.0, false
	// 12:GH.0, false
	// 000:12:CD.0, false
	// 12.ab.0, false
	// , false
}

func ExampleOptional() {
	tests := []string{
		"",
		"foo",
		"true",
	}

	for _, v := range tests {
		f := validate.Optional()
		fmt.Printf("%v ", f(v))

		f = validate.Optional(validate.IsBool)
		fmt.Printf("%v\n", f(v))
	}

	// Output: <nil> <nil>
	// <nil> Invalid value for a boolean "foo"
	// <nil> <nil>
}

func ExampleRequired() {
	tests := []string{
		"",
		"foo",
		"true",
	}

	for _, v := range tests {
		f := validate.Required()
		fmt.Printf("%v ", f(v))

		f = validate.Required(validate.IsBool)
		fmt.Printf("%v\n", f(v))
	}

	// Output: <nil> Invalid value for a boolean ""
	// <nil> Invalid value for a boolean "foo"
	// <nil> <nil>
}

func ExampleIsValidCPUSet() {
	tests := []string{
		"1",       // valid
		"1,2,3",   // valid
		"1-3",     // valid
		"1-3,4-6", // valid
		"1-3,4",   // valid
		"abc",     // invalid syntax
		"1-",      // invalid syntax
		"1,",      // invalid syntax
		"-1",      // invalid syntax
		",1",      // invalid syntax
		"1,2,3,3", // invalid: Duplicate CPU
		"1-2,2",   // invalid: Duplicate CPU
		"1-2,2-3", // invalid: Duplicate CPU
	}

	for _, t := range tests {
		err := validate.IsValidCPUSet(t)
		fmt.Printf("%v\n", err)
	}

	// Output: <nil>
	// <nil>
	// <nil>
	// <nil>
	// <nil>
	// Invalid CPU limit syntax
	// Invalid CPU limit syntax
	// Invalid CPU limit syntax
	// Invalid CPU limit syntax
	// Invalid CPU limit syntax
	// Cannot define CPU multiple times
	// Cannot define CPU multiple times
	// Cannot define CPU multiple times
}

func Test_IsInt64(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"123", true},
		{"-123", true},
		{"0", true},
		{"123abc", false},
		{"abc123", false},
		{"", false},
	}

	for _, test := range tests {
		err := validate.IsInt64(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsInt64(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsUint8(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"0", true},
		{"255", true},
		{"256", false},
		{"-1", false},
		{"100.5", false},
		{"abc", false},
	}

	for _, test := range tests {
		err := validate.IsUint8(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsUint8(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsUint16(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"0", true},
		{"65535", true},
		{"65536", false},
		{"-1", false},
		{"100.5", false},
		{"abc", false},
	}

	for _, test := range tests {
		err := validate.IsUint16(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsUint16(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsUint32(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"0", true},
		{"4294967295", true},
		{"4294967296", false},
		{"-1", false},
		{"100.5", false},
		{"abc", false},
	}

	for _, test := range tests {
		err := validate.IsUint32(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsUint32(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_ParseUint32Range(t *testing.T) {
	tests := []struct {
		value         string
		expectedStart uint32
		expectedSize  uint32
		err           bool
	}{
		{"1-5", 1, 5, false},
		{"2", 2, 1, false},
		{"", 0, 0, true},
		{" ", 0, 0, true},
		{"1-2-3", 0, 0, true},
		{"2-1", 0, 0, true},
		{"2-a", 0, 0, true},
		{"a-2", 0, 0, true},
		{"-1", 0, 0, true},
		{"-1-1", 0, 0, true},
		{"1 -1", 0, 0, true},
		{"abc", 0, 0, true},
	}

	for _, test := range tests {
		start, size, err := validate.ParseUint32Range(test.value)
		if (err != nil) != test.err {
			t.Errorf("ParseUint32Range(%q) error = %v, want error = %v", test.value, err, test.err)
		}

		if start != test.expectedStart {
			t.Errorf("ParseUint32Range(%q) start = %v, want %v", test.value, start, test.expectedStart)
		}

		if size != test.expectedSize {
			t.Errorf("ParseUint32Range(%q) size = %v, want %v", test.value, size, test.expectedSize)
		}
	}
}

func Benchmark_ParseUint32Range(b *testing.B) {
	for b.Loop() {
		_, _, _ = validate.ParseUint32Range("1-5")
		_, _, _ = validate.ParseUint32Range("1")
	}
}

func Test_IsUint32Range(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"1-5", true},
		{"2", true},
		{"0-10", true},
		{"10-0", false},  // invalid range
		{"a-b", false},   // non-numeric
		{"1-2-3", false}, // invalid format
		{"1-", false},    // incomplete range
		{"-1", false},    // negative number
		{"", false},      // empty string
	}

	for _, test := range tests {
		err := validate.IsUint32Range(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsUint32Range(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsInRange(t *testing.T) {
	// TODO
}

func Test_IsPriority(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"0", true},
		{"10", true},
		{"-1", false},
		{"1000", false},
		{"abc", false},
	}

	for _, test := range tests {
		err := validate.IsPriority(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsPriority(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Benchmark_IsPriority(b *testing.B) {
	for b.Loop() {
		_ = validate.IsPriority("10")
	}
}

func Test_IsBool(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"true", true},
		{"false", true},
		{"1", true},
		{"0", true},
		{"yes", true},
		{"no", true},
		{"maybe", false},
		{"2", false},
	}

	for _, test := range tests {
		err := validate.IsBool(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsBool(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsOneOf(t *testing.T) {
	// TODO
}

func Test_IsAny(t *testing.T) {
	tests := []string{
		"",
		"foo",
		"bar",
		"baz",
		"qux",
	}

	for _, test := range tests {
		err := validate.IsAny(test)
		if err != nil {
			t.Errorf("IsAny(%q) = %v, want %v", test, err, nil)
		}
	}
}

func Test_IsListOf(t *testing.T) {
	// TODO
}

func Test_IsNotEmpty(t *testing.T) {
	tests := []string{
		"foo",
		"bar",
		"",
		"   ",
	}

	for _, test := range tests {
		err := validate.IsNotEmpty(test)
		if (err == nil) != (test != "") {
			t.Errorf("IsNotEmpty(%q) = %v, want %v", test, err == nil, test != "")
		}
	}
}

func Test_IsSize(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"1KiB", true},
		{"1024", true},
		{"1MB", true},
		{"1GB", true},
		{"1PiB", true},
		{"-1KiB", false},
		{"abc", false},
		{"1.5KiB", false},
		{"1,5KiB", false},
	}

	for _, test := range tests {
		err := validate.IsSize(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsSize(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsDeviceID(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"1234", true},
		{"abcd", true},
		{"12ab", true},
		{"cdef", true},
		{"defg", false},
		{"0", false},
		{"1", false},
		{"12", false},
		{"123", false},
		{"-123", false},
		{"abc", false},
		{"123abc", false},
	}

	for _, test := range tests {
		err := validate.IsDeviceID(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsDeviceID(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsInterfaceName(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"eth0", true},
		{"wlan0", true},
		{"lo", true},
		{"eth0.10", true},
		{"eth-0", true},
		{"..eth0", false},
		{"1234", true},
		{"abcdefghijklmno", true},
		{"abcdefghijklmnop", false},
		{"eth,0", false},
		{"", false},
		{"a", false},
	}

	for _, test := range tests {
		err := validate.IsInterfaceName(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsInterfaceName(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsNetworkAddressCIDR(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{
			value:    "192.0.2.1/24",
			expected: true,
		},
		{
			value:    "2001:db8::1/64",
			expected: true,
		},
		{
			value:    "192.0.2.1",
			expected: false,
		},
		{
			value:    "2001:db8::1",
			expected: false,
		},
	}

	for _, test := range tests {
		err := validate.IsNetworkAddressCIDR(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsNetworkAddressCIDR(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsNetworkRange(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{
			value:    "192.0.2.1",
			expected: false,
		},
		{
			value:    "::1-::ffff",
			expected: true,
		},
		{
			value:    "192.0.2.1-192.0.2.2",
			expected: true,
		},
		{
			value:    "192.0.2.2-192.0.2.1",
			expected: false,
		},
		{
			value:    "192.0.2.1-2001:db8::1",
			expected: false,
		},
		{
			value:    "192.0.2.1/24",
			expected: false,
		},
		{
			value:    "start-192.0.2.2",
			expected: false,
		},
		{
			value:    "192.0.2.1-end",
			expected: false,
		},
	}

	for _, test := range tests {
		err := validate.IsNetworkRange(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsNetworkRange(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsNetworkV4(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{
			value:    "192.0.2.1",
			expected: false,
		},
		{
			value:    "192.0.2.1/24",
			expected: false,
		},
		{
			value:    "192.0.2.0/24",
			expected: true,
		},
		{
			value:    "2001:db8::1",
			expected: false,
		},
		{
			value:    "2001:db8::/64",
			expected: false,
		},
	}

	for _, test := range tests {
		err := validate.IsNetworkV4(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsNetworkV4(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsNetworkV6(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{
			value:    "192.0.2.1",
			expected: false,
		},
		{
			value:    "192.0.2.1/24",
			expected: false,
		},
		{
			value:    "192.0.2.0/24",
			expected: false,
		},
		{
			value:    "2001:db8::1",
			expected: false,
		},
		{
			value:    "2001:db8::1/64",
			expected: false,
		},
		{
			value:    "2001:db8::/64",
			expected: true,
		},
	}

	for _, test := range tests {
		err := validate.IsNetworkV6(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsNetworkV6(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsNetworkMAC(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"00:00:5e:00:53:01", true},
		{"02:00:5e:10:00:00:00:01", false}, // too long
		{"00-00-5e-00-53-01", false},       // invalid delimiter
		{"0000.5e00.5301", false},          // invalid delimiter
		{"invalid", false},
		{"", false},
	}

	for _, test := range tests {
		err := validate.IsNetworkMAC(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsNetworkMAC(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsNetworkAddress(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"192.0.2.1", true},
		{"2001:db8::1", true},
		{"192.0.2.0/32", false},
		{"192.0.2.256", false},
		{"2001:db8::1/128", false},
		{"2001:db8::g", false},
	}

	for _, test := range tests {
		err := validate.IsNetworkAddress(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsNetworkAddress(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsNetwork(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"192.0.2.0/24", true},
		{"192.0.2.0/32", true},
		{"192.0.2.1/32", true},
		{"192.0.2.1/24", false},
		{"192.0.2.0", false},
		{"192.0.2.1", false},
		{"2001:db8::/128", true},
		{"2001:db8::0/128", true},
		{"2001:db8::", false},
		{"2001:db8::0", false},
		{"2001:db8::0/64", true},
		{"2001:db8::1/64", false},
		{"2001:db8::1", false},
	}

	for _, test := range tests {
		err := validate.IsNetwork(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsNetwork(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsNetworkPort(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"80", true},
		{"0", true},
		{"65535", true},
		{"-1", false},
		{"65536", false},
		{"abc", false},
	}

	for _, test := range tests {
		err := validate.IsNetworkPort(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsNetworkPort(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}

func Test_IsNetworkPortRange(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"80", true},
		{"0", true},
		{"65535", true},
		{"80-90", true},
		{"0-65535", true},
		{"90-80", false},
		{"-1", false},
		{"65536", false},
		{"80-65536", false},
		{"abc", false},
	}

	for _, test := range tests {
		err := validate.IsNetworkPortRange(test.value)
		if (err == nil) != test.expected {
			t.Errorf("IsNetworkPortRange(%q) = %v, want %v", test.value, err == nil, test.expected)
		}
	}
}
