package ovn

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// unquote passes s through strconv.Unquote if the first character is a ", otherwise returns s unmodified.
// This is useful as openvswitch's tools can sometimes return values double quoted if they start with a number.
func unquote(s string) (string, error) {
	if strings.HasPrefix(s, `"`) {
		return strconv.Unquote(s)
	}

	return s, nil
}

// readCertFile reads a certificate or key file from the given path and returns its contents as a string.
// If the file doesn't exist, it returns an error message indicating that OVN is configured
// for SSL but the specified certificate component (e.g., CA cert, client cert, or client key) is missing.
// For other file reading errors, it returns the underlying error directly.
func readCertFile(path string, description string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("OVN configured to use SSL but no %s defined", description)
		}

		return "", err
	}

	return string(content), nil
}
