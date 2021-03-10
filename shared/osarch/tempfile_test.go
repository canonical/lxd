package osarch

import (
	"io/ioutil"
	"os"

	"github.com/stretchr/testify/suite"
)

// WriteTempFile writes content to a temporary file.
func WriteTempFile(s suite.Suite, dir string, prefix string, content string) (string, func()) {
	f, err := ioutil.TempFile(dir, prefix)
	if err != nil {
		s.T().Errorf("Failed to create temporary file: %v", err)
	}
	defer f.Close()

	_, err = f.WriteString(content)
	if err != nil {
		s.T().Errorf("Failed to write string to temp file: %v", err)
	}
	return f.Name(), func() { os.Remove(f.Name()) }
}
