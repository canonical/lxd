package libkrun

import (
	"errors"
	"syscall"
	"testing"
)

func TestCheckCodeErrnoMapping(t *testing.T) {
	err := checkCode(0)
	if err != nil {
		t.Fatalf("checkCode(0) = %v, want nil", err)
	}

	err = checkCode(1)
	if err != nil {
		t.Fatalf("checkCode(1) = %v, want nil", err)
	}

	err = checkCode(-int32(syscall.EINVAL))
	if err == nil {
		t.Fatal("checkCode(-EINVAL) = nil, want error")
	}

	var eno Errno
	if !errors.As(err, &eno) {
		t.Fatalf("checkCode(-EINVAL) error type = %T, want Errno", err)
	}

	if syscall.Errno(eno) != syscall.EINVAL {
		t.Fatalf("errno = %v, want %v", syscall.Errno(eno), syscall.EINVAL)
	}
}
