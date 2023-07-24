package revert_test

import (
	"fmt"

	"github.com/canonical/lxd/lxd/revert"
)

// Example of how to use the revert package to fail a function and run revert functions in reverse order
func ExampleReverter_fail() {
	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() { fmt.Println("1st step") })
	revert.Add(func() { fmt.Println("2nd step") })

	// Revert functions are run in reverse order on return.
	// Output: 2nd step
	// 1st step
}

// Example of how to use revert to succeed a function
func ExampleReverter_success() {
	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() { fmt.Println("1st step") })
	revert.Add(func() { fmt.Println("2nd step") })

	revert.Success() // Revert functions added are not run on return.
	// Output:
}
