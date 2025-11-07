//go:build !linux || !cgo

package validate

import (
	"fmt"
)

// IsBPFDelegationOption validates a BPF Token delegation option.
func IsBPFDelegationOption(delegateOption string) func(value string) error {
	return func(value string) error {
		return fmt.Errorf("This should never be called")
	}
}
