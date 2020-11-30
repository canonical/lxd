package revert_test

import (
	"fmt"

	"github.com/lxc/lxd/lxd/revert"
)

func ExampleReverter_fail() {
	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() { fmt.Println("1st step") })
	revert.Add(func() { fmt.Println("2nd step") })

	return // Revert functions are run in reverse order on return.
	// Output: 2nd step
	// 1st step
}

func ExampleReverter_success() {
	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() { fmt.Println("1st step") })
	revert.Add(func() { fmt.Println("2nd step") })

	revert.Success() // Revert functions added are not run on return.
	return
	// Output:
}
