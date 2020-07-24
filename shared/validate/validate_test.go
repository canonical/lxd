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
