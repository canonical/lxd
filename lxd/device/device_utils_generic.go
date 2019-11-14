package device

import (
	"strings"
)

// deviceNameEncode encodes a string to be used as part of a file name in the LXD devices path.
// The encoding scheme replaces "-" with "--" and then "/" with "-".
func deviceNameEncode(text string) string {
	return strings.Replace(strings.Replace(text, "-", "--", -1), "/", "-", -1)
}

// deviceNameDecode decodes a string used in the LXD devices path back to its original form.
// The decoding scheme converts "-" back to "/" and "--" back to "-".
func deviceNameDecode(text string) string {
	// This converts "--" to the null character "\0" first, to allow remaining "-" chars to be
	// converted back to "/" before making a final pass to convert "\0" back to original "-".
	return strings.Replace(strings.Replace(strings.Replace(text, "--", "\000", -1), "-", "/", -1), "\000", "-", -1)
}

// deviceJoinPath joins together prefix and text delimited by a "." for device path generation.
func deviceJoinPath(parts ...string) string {
	return strings.Join(parts, ".")
}
