package rsync

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test assertSafePath.
func TestAssertSafePath(t *testing.T) {
	// Absolute paths without ".." segments are accepted.
	assert.NoError(t, assertSafePath("/"))
	assert.NoError(t, assertSafePath("/var/lib/lxd/storage-pools/default/custom/vol"))
	assert.NoError(t, assertSafePath("/var/lib/lxd/storage-pools/default/custom/vol/"))

	// A ".." that is part of a path component (not its own segment) is fine.
	assert.NoError(t, assertSafePath("/var/lib/lxd/vol..name"))

	// Non-absolute paths are rejected.
	assert.Error(t, assertSafePath(""))
	assert.Error(t, assertSafePath("."))
	assert.Error(t, assertSafePath("relative/path"))

	// Option-like paths, which rsync could otherwise interpret as flags, are
	// rejected because they are not absolute.
	assert.Error(t, assertSafePath("-e/tmp/x"))
	assert.Error(t, assertSafePath("--rsh=/tmp/x"))
	assert.Error(t, assertSafePath("--rsync-path=touch /tmp/pwn"))

	// Paths containing a ".." segment, which could traverse outside of the
	// intended directory, are rejected.
	assert.Error(t, assertSafePath("/foo/bar/baz/../../../etc/passwd"))
	assert.Error(t, assertSafePath("/var/lib/lxd/storage-pools/default/custom/../../../../etc"))
	assert.Error(t, assertSafePath("/.."))
}

// Test that the rsync entry points reject option-like paths before invoking
// rsync, so that a path beginning with "-" can never be treated as an option.
func TestRsyncRejectsOptionLikePaths(t *testing.T) {
	// A path beginning with "-" looks like an rsync option such as --rsh.
	const optionLikePath = "--rsh=/tmp/x"

	_, err := LocalCopy(optionLikePath, "/tmp/dest", "", false)
	assert.ErrorContains(t, err, "must be absolute")

	_, err = CopyFile(optionLikePath, "/tmp/dest", "", false)
	assert.ErrorContains(t, err, "must be absolute")

	err = Send("vol", optionLikePath, nil, nil, nil, "", "")
	assert.ErrorContains(t, err, "must be absolute")

	err = Recv(optionLikePath, nil, nil, nil)
	assert.ErrorContains(t, err, "must be absolute")
}

// Test that the rsync entry points reject paths containing ".." segments before
// invoking rsync, so that a crafted path cannot traverse outside of its
// intended directory.
func TestRsyncRejectsTraversalPaths(t *testing.T) {
	// A path with ".." segments could otherwise escape the source or
	// destination directory.
	const traversalPath = "/var/lib/lxd/storage-pools/default/custom/../../../../etc"

	_, err := LocalCopy(traversalPath, "/tmp/dest", "", false)
	assert.ErrorContains(t, err, "must not contain")

	_, err = CopyFile(traversalPath, "/tmp/dest", "", false)
	assert.ErrorContains(t, err, "must not contain")

	err = Send("vol", traversalPath, nil, nil, nil, "", "")
	assert.ErrorContains(t, err, "must not contain")

	err = Recv(traversalPath, nil, nil, nil)
	assert.ErrorContains(t, err, "must not contain")
}
