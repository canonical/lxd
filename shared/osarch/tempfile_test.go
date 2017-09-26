package osarch

import (
	"os"

	"github.com/stretchr/testify/suite"

	"github.com/lxc/lxd/shared"
)

// WriteTempFile writes content to a temporary file.
func WriteTempFile(s suite.Suite, dir string, prefix string, content string) (string, func()) {
	filename, err := shared.WriteTempFile(dir, prefix, content)
	if err != nil {
		s.T().Fatalf("failed to create temporary file: %v", err)
	}

	return filename, func() { os.Remove(filename) }
}
