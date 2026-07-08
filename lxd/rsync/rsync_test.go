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

// Test assertSafeName.
func TestAssertSafeName(t *testing.T) {
	// Names without whitespace or control characters are accepted, including
	// the project separator, snapshot delimiter, and characters that are valid
	// in storage volume names but harmless in the non-shell rsync "-e" command.
	assert.NoError(t, assertSafeName("vol"))
	assert.NoError(t, assertSafeName("default_vol"))
	assert.NoError(t, assertSafeName("default_vol/snap0"))
	assert.NoError(t, assertSafeName("valid@volume"))
	assert.NoError(t, assertSafeName("valid#volume"))
	assert.NoError(t, assertSafeName("valid;volume"))
	assert.NoError(t, assertSafeName("valid&volume"))

	// A ".." within a segment (not a whole segment) is fine.
	assert.NoError(t, assertSafeName("valid..volume"))

	// Empty names are rejected.
	assert.Error(t, assertSafeName(""))

	// Names containing whitespace, which rsync would split into additional
	// arguments to the remote command, are rejected.
	assert.Error(t, assertSafeName("invalid volume"))
	assert.Error(t, assertSafeName("invalid\tvolume"))
	assert.Error(t, assertSafeName("invalid\nvolume"))
	assert.Error(t, assertSafeName("trailing "))

	// Names containing control characters are rejected.
	assert.Error(t, assertSafeName("invalid\x00volume"))

	// Non-ASCII whitespace, separator, control and format characters are
	// rejected too (the check is Unicode-aware, not ASCII-only), while
	// legitimate non-ASCII letters are accepted.
	assert.NoError(t, assertSafeName("v\u00f6lume"))       // o with diaeresis.
	assert.Error(t, assertSafeName("invalid\u00a0volume")) // No-break space.
	assert.Error(t, assertSafeName("invalid\u2028volume")) // Line separator.
	assert.Error(t, assertSafeName("invalid\u202evolume")) // Right-to-left override.
	assert.Error(t, assertSafeName("invalid\u200bvolume")) // Zero-width space.

	// Names beginning with "-" are allowed: Send passes "--" to "lxd netcat"
	// before the positional arguments, so cobra never parses the name as a flag.
	// ValidVolumeName permits leading hyphens, so rejecting them here would
	// break migration of such volumes.
	assert.NoError(t, assertSafeName("-vol"))
	assert.NoError(t, assertSafeName("-valid-volume"))

	// Absolute names, which could escape the log directory used by
	// "lxd netcat", are rejected.
	assert.Error(t, assertSafeName("/vol"))
	assert.Error(t, assertSafeName("/etc/passwd"))

	// Names that traverse outside of the intended log directory used by
	// "lxd netcat" are rejected.
	assert.Error(t, assertSafeName(".."))
	assert.Error(t, assertSafeName("../etc"))
	assert.Error(t, assertSafeName("vol/../../etc"))
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

	_, err = LocalCopy("/tmp/src", optionLikePath, "", false)
	assert.ErrorContains(t, err, "must be absolute")

	_, err = CopyFile("/tmp/src", optionLikePath, "", false)
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

	_, err = LocalCopy("/tmp/src", traversalPath, "", false)
	assert.ErrorContains(t, err, "must not contain")

	_, err = CopyFile("/tmp/src", traversalPath, "", false)
	assert.ErrorContains(t, err, "must not contain")

	err = Send("vol", traversalPath, nil, nil, nil, "", "")
	assert.ErrorContains(t, err, "must not contain")

	err = Recv(traversalPath, nil, nil, nil)
	assert.ErrorContains(t, err, "must not contain")
}

// Test that Send rejects a name containing whitespace or control characters
// before invoking rsync, so that a crafted name can never inject additional
// arguments into the rsync "-e" remote shell command.
func TestSendRejectsUnsafeNames(t *testing.T) {
	// A name containing whitespace would be split by rsync into extra arguments
	// to the remote command.
	err := Send("vol name", "/tmp/src", nil, nil, nil, "", "")
	assert.ErrorContains(t, err, "whitespace or control characters")

	// An empty name is rejected too.
	err = Send("", "/tmp/src", nil, nil, nil, "", "")
	assert.ErrorContains(t, err, "whitespace or control characters")

	// A name with ".." segments could escape the log directory used by
	// "lxd netcat".
	err = Send("vol/../../etc", "/tmp/src", nil, nil, nil, "", "")
	assert.ErrorContains(t, err, "local path")
}
