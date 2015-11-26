// +build 386 arm ppc s390

package main

const (
	/* This is really 0x9123683E, go wants us to give it in signed form
	 * since we use it as a signed constant. */
	filesystemSuperMagicBtrfs = -1859950530
)
