package shared

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestDetectCompressionFile(t *testing.T) {
	tests := []struct {
		name               string
		setupHeader        func([]byte)
		expectedTarArgs    []string
		expectedExt        string
		expectedDecompress []string
		expectedError      string
	}{
		{
			name: "bzip2",
			setupHeader: func(h []byte) {
				h[0] = 'B'
				h[1] = 'Z'
			},
			expectedTarArgs:    []string{"-jxf"},
			expectedExt:        ".tar.bz2",
			expectedDecompress: []string{"bzip2", "-d"},
		},
		{
			name: "gzip",
			setupHeader: func(h []byte) {
				h[0] = 0x1f
				h[1] = 0x8b
			},
			expectedTarArgs:    []string{"-zxf"},
			expectedExt:        ".tar.gz",
			expectedDecompress: []string{"gzip", "-d"},
		},
		{
			name: "xz",
			setupHeader: func(h []byte) {
				h[0] = 0xFD
				h[1] = '7'
				h[2] = 'z'
				h[3] = 'X'
				h[4] = 'Z'
			},
			expectedTarArgs:    []string{"-Jxf"},
			expectedExt:        ".tar.xz",
			expectedDecompress: []string{"xz", "-d"},
		},
		{
			name: "lzma",
			setupHeader: func(h []byte) {
				h[0] = 0x00
				h[1] = '7'
				h[2] = 'z'
				h[3] = 'X'
				h[4] = 'Z'
			},
			expectedTarArgs:    []string{"--lzma", "-xf"},
			expectedExt:        ".tar.lzma",
			expectedDecompress: []string{"lzma", "-d"},
		},
		{
			name: "lzma_alt",
			setupHeader: func(h []byte) {
				h[0] = 0x5d
				h[1] = 0x00
				h[2] = 0x00
			},
			expectedTarArgs:    []string{"--lzma", "-xf"},
			expectedExt:        ".tar.lzma",
			expectedDecompress: []string{"lzma", "-d"},
		},
		{
			name: "tar",
			setupHeader: func(h []byte) {
				copy(h[257:262], []byte("ustar"))
			},
			expectedTarArgs:    []string{"-xf"},
			expectedExt:        ".tar",
			expectedDecompress: []string{},
		},
		{
			name: "squashfs",
			setupHeader: func(h []byte) {
				h[0] = 'h'
				h[1] = 's'
				h[2] = 'q'
				h[3] = 's'
			},
			expectedTarArgs:    []string{"-xf"},
			expectedExt:        ".squashfs",
			expectedDecompress: []string{"sqfs2tar", "--no-skip"},
		},
		{
			name: "qcow2",
			setupHeader: func(h []byte) {
				h[0] = 'Q'
				h[1] = 'F'
				h[2] = 'I'
			},
			expectedTarArgs:    []string{""},
			expectedExt:        ".qcow2",
			expectedDecompress: []string{"qemu-img", "convert", "-O", "raw"},
		},
		{
			name: "zstd",
			setupHeader: func(h []byte) {
				h[0] = 0x28
				h[1] = 0xb5
				h[2] = 0x2f
				h[3] = 0xfd
			},
			expectedTarArgs:    []string{"--zstd", "-xf"},
			expectedExt:        ".tar.zst",
			expectedDecompress: []string{"zstd", "-d"},
		},
		{
			name: "unsupported",
			setupHeader: func(h []byte) {
				h[0] = 0xFF
				h[1] = 0xFF
			},
			expectedError: "Unsupported compression",
		},
		{
			name: "read_error",
			setupHeader: func(h []byte) {
			},
			expectedError: "read error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reader io.Reader

			if tt.name == "read_error" {
				reader = &errorReader{err: errors.New("read error")}
			} else {
				header := make([]byte, 263)
				tt.setupHeader(header)
				reader = bytes.NewReader(header)
			}

			tarArgs, ext, decompressCmd, err := DetectCompressionFile(reader)

			if tt.expectedError != "" {
				if err == nil {
					t.Fatalf("Expected error %q, got nil", tt.expectedError)
				}

				if err.Error() != tt.expectedError {
					t.Fatalf("Expected error %q, got: %v", tt.expectedError, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}

			if len(tarArgs) != len(tt.expectedTarArgs) {
				t.Fatalf("Expected tar args %v, got: %v", tt.expectedTarArgs, tarArgs)
			}

			for i := range tt.expectedTarArgs {
				if tarArgs[i] != tt.expectedTarArgs[i] {
					t.Fatalf("Expected tar args %v, got: %v", tt.expectedTarArgs, tarArgs)
				}
			}

			if ext != tt.expectedExt {
				t.Fatalf("Expected extension %s, got: %s", tt.expectedExt, ext)
			}

			if len(decompressCmd) != len(tt.expectedDecompress) {
				t.Fatalf("Expected decompress cmd %v, got: %v", tt.expectedDecompress, decompressCmd)
			}

			for i := range tt.expectedDecompress {
				if decompressCmd[i] != tt.expectedDecompress[i] {
					t.Fatalf("Expected decompress cmd %v, got: %v", tt.expectedDecompress, decompressCmd)
				}
			}
		})
	}
}

// errorReader is a mock reader that always returns an error.
type errorReader struct {
	err error
}

// Read implements [io.Reader] and always returns the configured error.
func (r *errorReader) Read(p []byte) (int, error) {
	return 0, r.err
}
