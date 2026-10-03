package instancetype

import (
	"testing"
)

func TestTypeImageType(t *testing.T) {
	tests := []struct {
		instanceType Type
		want         Type
	}{
		{instanceType: Container, want: Container},
		{instanceType: VM, want: VM},
		{instanceType: MicroVM, want: Container},
		{instanceType: Any, want: Any},
	}

	for _, tc := range tests {
		got := tc.instanceType.ImageType()
		if got != tc.want {
			t.Errorf("Type(%d).ImageType() = %d, want %d", tc.instanceType, got, tc.want)
		}
	}
}
