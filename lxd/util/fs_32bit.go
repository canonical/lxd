//go:build 386 || arm || ppc || s390 || mips || mipsle
// +build 386 arm ppc s390 mips mipsle

package util

const (
	// FilesystemSuperMagicBtrfs is the 32bit magic for Btrfs (as signed constant)
	FilesystemSuperMagicBtrfs = -1859950530
)
