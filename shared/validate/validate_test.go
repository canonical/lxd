package validate_test

import (
	"fmt"

	"github.com/lxc/lxd/shared/validate"
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
		"0000:12:CD.0", // upper-case not supported
		"12:ab.0",      // valid
		"12:CD.0",      // upper-case not supported
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
	// 0000:12:CD.0, false
	// 12:ab.0, true
	// 12:CD.0, false
	// 0000:12:gh.0, false
	// 0000:12:GH.0, false
	// 12:gh.0, false
	// 12:GH.0, false
	// 000:12:CD.0, false
	// 12.ab.0, false
	// , false
}
