package backup

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/canonical/lxd/lxd/archive"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
)

// TarReader rewinds backup file handle r and returns new tar reader and process cleanup function.
func TarReader(r io.ReadSeeker, sysOS *sys.OS, outputPath string) (*tar.Reader, context.CancelFunc, error) {
	_, err := r.Seek(0, io.SeekStart)
	if err != nil {
		return nil, nil, err
	}

	_, _, unpacker, err := shared.DetectCompressionFile(r)
	if err != nil {
		return nil, nil, err
	}

	if unpacker == nil {
		return nil, nil, errors.New("Unsupported backup compression")
	}

	tr, cancelFunc, err := archive.CompressedTarReader(context.Background(), r, unpacker, sysOS, outputPath)
	if err != nil {
		return nil, nil, err
	}

	return tr, cancelFunc, nil
}

// ValidateBackupName validates the given backup name.
// If the name is legal, then the legal name and a nil error are returned.
// If the name is illegal, then an empty string and an error are returned.
func ValidateBackupName(backupName string) (string, error) {
	if strings.Contains(backupName, "/") {
		return "", errors.New("Backup name must not contain forward slashes")
	}

	if strings.Contains(backupName, "\\") {
		return "", errors.New("Backup name must not contain back slashes")
	}

	if strings.Contains(backupName, "..") {
		return "", errors.New("Backup name must not contain '..'")
	}

	return backupName, nil
}
