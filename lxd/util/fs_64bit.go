//go:build amd64 || ppc64 || ppc64le || arm64 || s390x || mips64 || mips64le || riscv64
// +build amd64 ppc64 ppc64le arm64 s390x mips64 mips64le riscv64

package util

const (
	// FilesystemSuperMagicBtrfs is the 64bit magic for Btrfs
	FilesystemSuperMagicBtrfs = 0x9123683E
)
