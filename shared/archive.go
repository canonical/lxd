package shared

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// DetectCompression detects compression from a file name.
func DetectCompression(fname string) ([]string, string, []string, error) {
	f, err := os.Open(fname)
	if err != nil {
		return nil, "", nil, err
	}
	defer f.Close()

	return DetectCompressionFile(f)
}

// DetectCompressionFile detects the compression type of a file and returns the tar arguments needed
// to unpack the file, compression type (in the form of a file extension), and the command needed
// to decompress the file to an uncompressed tarball.
func DetectCompressionFile(f io.Reader) ([]string, string, []string, error) {
	// read header parts to detect compression method
	// bz2 - 2 bytes, 'BZ' signature/magic number
	// gz - 2 bytes, 0x1f 0x8b
	// lzma - 6 bytes, { [0x000, 0xE0], '7', 'z', 'X', 'Z', 0x00 } -
	// xy - 6 bytes,  header format { 0xFD, '7', 'z', 'X', 'Z', 0x00 }
	// tar - 263 bytes, trying to get ustar from 257 - 262
	header := make([]byte, 263)
	_, err := f.Read(header)
	if err != nil {
		return nil, "", nil, err
	}

	switch {
	case bytes.Equal(header[0:2], []byte{'B', 'Z'}):
		return []string{"-jxf"}, ".tar.bz2", []string{"bzip2", "-d"}, nil
	case bytes.Equal(header[0:2], []byte{0x1f, 0x8b}):
		return []string{"-zxf"}, ".tar.gz", []string{"gzip", "-d"}, nil
	case (bytes.Equal(header[1:5], []byte{'7', 'z', 'X', 'Z'}) && header[0] == 0xFD):
		return []string{"-Jxf"}, ".tar.xz", []string{"xz", "-d"}, nil
	case (bytes.Equal(header[1:5], []byte{'7', 'z', 'X', 'Z'}) && header[0] != 0xFD):
		return []string{"--lzma", "-xf"}, ".tar.lzma", []string{"lzma", "-d"}, nil
	case bytes.Equal(header[0:3], []byte{0x5d, 0x00, 0x00}):
		return []string{"--lzma", "-xf"}, ".tar.lzma", []string{"lzma", "-d"}, nil
	case bytes.Equal(header[257:262], []byte{'u', 's', 't', 'a', 'r'}):
		return []string{"-xf"}, ".tar", []string{}, nil
	case bytes.Equal(header[0:4], []byte{'h', 's', 'q', 's'}):
		return []string{"-xf"}, ".squashfs", []string{"sqfs2tar", "--no-skip"}, nil
	case bytes.Equal(header[0:3], []byte{'Q', 'F', 'I'}):
		return []string{""}, ".qcow2", []string{"qemu-img", "convert", "-O", "raw"}, nil
	case bytes.Equal(header[0:4], []byte{0x28, 0xb5, 0x2f, 0xfd}):
		return []string{"--zstd", "-xf"}, ".tar.zst", []string{"zstd", "-d"}, nil
	default:
		return nil, "", nil, fmt.Errorf("Unsupported compression")
	}
}

// CompressedTarReader returns a tar reader from the supplied (optionally compressed) tarball stream.
// The unpacker arguments are those returned by DetectCompressionFile().
// The returned cancelFunc should be called when finished with reader to clean up any resources used. This can be
// done before reading to the end of the tarball if desired.
func CompressedTarReader(ctx context.Context, r io.ReadSeeker, unpacker []string) (*tar.Reader, context.CancelFunc, error) {
	ctx, cancelFunc := context.WithCancel(ctx)

	r.Seek(0, 0)
	var tr *tar.Reader

	if len(unpacker) > 0 {
		cmd := exec.CommandContext(ctx, unpacker[0], unpacker[1:]...)
		cmd.Stdin = r
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, cancelFunc, err
		}

		err = cmd.Start()
		if err != nil {
			stdout.Close()
			return nil, cancelFunc, err
		}

		ctxCancelFunc := cancelFunc

		// Now that unpacker process has started, wrap context cancel function with one that waits for
		// the unpacker process to complete.
		cancelFunc = func() {
			ctxCancelFunc()
			cmd.Wait()
		}

		tr = tar.NewReader(stdout)
	} else {
		tr = tar.NewReader(r)
	}

	return tr, cancelFunc, nil
}
