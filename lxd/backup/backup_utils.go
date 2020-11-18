package backup

import (
	"archive/tar"
	"context"
	"fmt"
	"io"

	"github.com/grant-he/lxd/shared"
)

// TarReader rewinds backup file handle r and returns new tar reader and process cleanup function.
func TarReader(r io.ReadSeeker) (*tar.Reader, context.CancelFunc, error) {
	r.Seek(0, 0)
	_, _, unpacker, err := shared.DetectCompressionFile(r)
	if err != nil {
		return nil, nil, err
	}

	if unpacker == nil {
		return nil, nil, fmt.Errorf("Unsupported backup compression")
	}

	tr, cancelFunc, err := shared.CompressedTarReader(context.Background(), r, unpacker)
	if err != nil {
		return nil, nil, err
	}

	return tr, cancelFunc, nil
}
