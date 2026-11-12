package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/instance/drivers/libkrun"
)

func TestParseHWAddr(t *testing.T) {
	tests := []struct {
		hwaddr string
		mac    [6]byte
		err    bool
	}{
		{
			hwaddr: "00:16:3e:11:22:33",
			mac:    [6]byte{0x00, 0x16, 0x3e, 0x11, 0x22, 0x33},
			err:    false,
		},
		{
			hwaddr: "aa:bb:cc:dd:ee:ff",
			mac:    [6]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
			err:    false,
		},
		{
			hwaddr: "invalid-format",
			err:    true,
		},
		{
			hwaddr: "",
			err:    true,
		},
		{
			hwaddr: "00:16:3e:11:22:33:44:55",
			err:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.hwaddr, func(t *testing.T) {
			mac, err := parseHWAddr(tt.hwaddr)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.mac, mac)
			}
		})
	}
}

func TestDetectKernelFormat(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name     string
		magic    []byte
		expected libkrun.KernelFormat
		err      bool
	}{
		{
			name:     "ELF vmlinux",
			magic:    []byte{0x7f, 'E', 'L', 'F'},
			expected: libkrun.KernelFormatELF,
			err:      false,
		},
		{
			name:     "x86 PE bzImage",
			magic:    []byte{'M', 'Z', 0x00, 0x00},
			expected: libkrun.KernelFormatPEGZ,
			err:      false,
		},
		{
			name:     "gzip image",
			magic:    []byte{0x1f, 0x8b, 0x08, 0x00},
			expected: libkrun.KernelFormatImageGZ,
			err:      false,
		},
		{
			name:     "zstd image",
			magic:    []byte{0x28, 0xb5, 0x2f, 0xfd},
			expected: libkrun.KernelFormatImageZstd,
			err:      false,
		},
		{
			name:     "bzip2 image",
			magic:    []byte{'B', 'Z', 'h', '9'},
			expected: libkrun.KernelFormatImageBZ2,
			err:      false,
		},
		{
			name:     "unknown image format",
			magic:    []byte{0x00, 0x00, 0x00, 0x00},
			expected: 0,
			err:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := filepath.Join(tempDir, "kernel-"+tt.name)
			require.NoError(t, os.WriteFile(filePath, tt.magic, 0600))

			format, err := detectKernelFormat(filePath)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, format)
			}
		})
	}
}

func TestKernelFormat(t *testing.T) {
	names := map[string]libkrun.KernelFormat{
		"raw":        libkrun.KernelFormatRaw,
		"elf":        libkrun.KernelFormatELF,
		"pe_gz":      libkrun.KernelFormatPEGZ,
		"image_gz":   libkrun.KernelFormatImageGZ,
		"image_bz2":  libkrun.KernelFormatImageBZ2,
		"image_zstd": libkrun.KernelFormatImageZstd,
	}

	// Explicit format names do not need to read the kernel.
	for name, want := range names {
		t.Run(name, func(t *testing.T) {
			got, err := kernelFormat(name, "/nonexistent/kernel")
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}

	t.Run("unsupported name", func(t *testing.T) {
		_, err := kernelFormat("bzImage", "/nonexistent/kernel")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `Unsupported kernel format "bzImage"`)
	})

	// "auto" and the empty name detect the format from the kernel image.
	kernelPath := filepath.Join(t.TempDir(), "vmlinux")
	require.NoError(t, os.WriteFile(kernelPath, []byte{0x7f, 'E', 'L', 'F', 0, 0, 0, 0}, 0600))

	for _, name := range []string{"auto", ""} {
		t.Run("detect with name "+strconv.Quote(name), func(t *testing.T) {
			got, err := kernelFormat(name, kernelPath)
			require.NoError(t, err)
			assert.Equal(t, libkrun.KernelFormatELF, got)

			_, err = kernelFormat(name, "/nonexistent/kernel")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "for format detection")
		})
	}
}

func TestScanConsoleForHalt(t *testing.T) {
	msg := guestHaltedMessage

	t.Run("message in one chunk", func(t *testing.T) {
		found, tail := scanConsoleForHalt(nil, []byte("[    1.234] Run /sbin/init as init process\r\n"))
		assert.False(t, found)

		found, _ = scanConsoleForHalt(tail, []byte("[   42.000] "+msg+"\r\n"))
		assert.True(t, found)
	})

	t.Run("message split across two chunks at every position", func(t *testing.T) {
		for i := 1; i < len(msg); i++ {
			found, tail := scanConsoleForHalt(nil, []byte("some output\n"+msg[:i]))
			assert.False(t, found, "split at %d", i)

			found, _ = scanConsoleForHalt(tail, []byte(msg[i:]+"\n"))
			assert.True(t, found, "split at %d", i)
		}
	})

	t.Run("message arriving one byte at a time", func(t *testing.T) {
		var tail []byte
		var found bool

		for i := range len(msg) {
			found, tail = scanConsoleForHalt(tail, []byte{msg[i]})
			if i < len(msg)-1 {
				assert.False(t, found, "byte %d", i)
			}
		}

		assert.True(t, found)
	})

	t.Run("no message", func(t *testing.T) {
		var tail []byte
		var found bool

		for _, chunk := range []string{"reboot: Restarting system\n", msg[:len(msg)-1] + "\n", msg[1:] + "\n"} {
			found, tail = scanConsoleForHalt(tail, []byte(chunk))
			assert.False(t, found, "chunk %q", chunk)
		}
	})

	t.Run("memory stays bounded without newlines", func(t *testing.T) {
		var tail []byte
		var found bool

		chunk := bytes.Repeat([]byte{'A'}, 32768)

		for range 1000 {
			found, tail = scanConsoleForHalt(tail, chunk)
			require.False(t, found)
			require.Less(t, len(tail), len(msg))
		}

		assert.LessOrEqual(t, cap(tail), 2*(len(chunk)+len(msg)))

		// The message is still found after a long stream without newlines.
		found, _ = scanConsoleForHalt(tail, []byte(msg))
		assert.True(t, found)
	})
}
