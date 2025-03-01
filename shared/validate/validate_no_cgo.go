//go:build !(linux && cgo)

package validate

import (
	"fmt"
)

// IsBpfDelegateOption validates a BPF Token delegation option.
func IsBpfDelegateOption(delegateOption string) func(value string) error {
	return func(value string) error {
		return fmt.Errorf("This should never be called.")
	}
}
