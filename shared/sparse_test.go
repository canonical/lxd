//go:build linux

package shared

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"testing/iotest"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

var _ SparseFile = (*sftp.File)(nil)

// sparseTestData returns random blocks separated by zero runs, ending with a
// zero run so that the final file size relies on truncation.
func sparseTestData(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer

	for range 4 {
		block := make([]byte, 32*1024)
		_, err := rand.Read(block)
		require.NoError(t, err)

		_, err = buf.Write(block)
		require.NoError(t, err)

		_, err = buf.Write(make([]byte, 256*1024))
		require.NoError(t, err)
	}

	_, err := buf.Write(make([]byte, 1<<20))
	require.NoError(t, err)

	return buf.Bytes()
}

func requireSparse(t *testing.T, path string, size int64) {
	t.Helper()

	var stat unix.Stat_t
	err := unix.Stat(path, &stat)
	require.NoError(t, err)

	require.Equal(t, size, stat.Size)
	require.Less(t, stat.Blocks*512, size/2, "File is not sparse")
}

func TestAlignedReader(t *testing.T) {
	src := bytes.Repeat([]byte{'x'}, 100*1024)

	tests := []struct {
		name   string
		offset int64
		first  int
	}{
		{name: "start", offset: 0, first: 32 * 1024},
		{name: "block-aligned", offset: sparseBlockSize, first: 32 * 1024},
		{name: "unaligned", offset: 13, first: sparseBlockSize - 13},
		{name: "unaligned-near-block", offset: sparseBlockSize + 1, first: sparseBlockSize - 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := newAlignedReader(bytes.NewReader(src), tt.offset)
			buf := make([]byte, 32*1024)

			n, err := reader.Read(buf)
			require.NoError(t, err)
			require.Equal(t, tt.first, n)

			total := n
			for {
				n, err = reader.Read(buf)
				total += n

				if err == io.EOF {
					break
				}

				require.NoError(t, err)
			}

			require.Equal(t, len(src), total)
		})
	}
}

func TestCopySparse(t *testing.T) {
	src := sparseTestData(t)
	path := filepath.Join(t.TempDir(), "sparse")

	f, err := os.Create(path)
	require.NoError(t, err)

	t.Cleanup(func() { _ = f.Close() })

	written, err := CopySparse(f, bytes.NewReader(src))
	require.NoError(t, err)
	require.EqualValues(t, len(src), written)

	err = f.Close()
	require.NoError(t, err)

	dst, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, src, dst)

	requireSparse(t, path, int64(len(src)))
}

func TestCopySparseZeroContent(t *testing.T) {
	src := make([]byte, 1<<20)
	path := filepath.Join(t.TempDir(), "sparse")

	f, err := os.Create(path)
	require.NoError(t, err)

	t.Cleanup(func() { _ = f.Close() })

	written, err := CopySparse(f, bytes.NewReader(src))
	require.NoError(t, err)
	require.EqualValues(t, len(src), written)

	err = f.Close()
	require.NoError(t, err)

	dst, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, src, dst)

	requireSparse(t, path, int64(len(src)))
}

func TestCopySparseExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparse")
	existing := bytes.Repeat([]byte{'A'}, 256*1024)

	f, err := os.Create(path)
	require.NoError(t, err)

	t.Cleanup(func() { _ = f.Close() })

	_, err = f.Write(existing)
	require.NoError(t, err)

	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)

	src := make([]byte, len(existing)/2)
	copy(src, "start")
	copy(src[len(src)-5:], "end")

	written, err := CopySparse(f, bytes.NewReader(src))
	require.NoError(t, err)
	require.EqualValues(t, len(src), written)

	err = f.Close()
	require.NoError(t, err)

	dst, err := os.ReadFile(path)
	require.NoError(t, err)

	expected := append([]byte{}, existing...)
	copy(expected, src)
	require.Equal(t, expected, dst)
}

func TestCopySparseShortReads(t *testing.T) {
	src := sparseTestData(t)
	path := filepath.Join(t.TempDir(), "sparse")

	f, err := os.Create(path)
	require.NoError(t, err)

	t.Cleanup(func() { _ = f.Close() })

	written, err := CopySparse(f, iotest.OneByteReader(bytes.NewReader(src)))
	require.NoError(t, err)
	require.EqualValues(t, len(src), written)

	err = f.Close()
	require.NoError(t, err)

	dst, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, src, dst)

	requireSparse(t, path, int64(len(src)))
}

func TestCopySparseAppend(t *testing.T) {
	prefix := []byte("existing data")
	src := sparseTestData(t)
	path := filepath.Join(t.TempDir(), "sparse")

	f, err := os.Create(path)
	require.NoError(t, err)

	t.Cleanup(func() { _ = f.Close() })

	_, err = f.Write(prefix)
	require.NoError(t, err)

	_, err = f.Seek(0, io.SeekEnd)
	require.NoError(t, err)

	written, err := CopySparse(f, bytes.NewReader(src))
	require.NoError(t, err)
	require.EqualValues(t, len(src), written)

	err = f.Close()
	require.NoError(t, err)

	dst, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, append(append([]byte{}, prefix...), src...), dst)

	requireSparse(t, path, int64(len(prefix)+len(src)))
}

// splitWriteSeeker forces writes to be split into small chunks.
type splitWriteSeeker struct {
	file io.WriteSeeker
}

func (w *splitWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	return w.file.Seek(offset, whence)
}

func (w *splitWriteSeeker) Write(p []byte) (int, error) {
	written := 0

	for written < len(p) {
		size := min(7, len(p)-written)

		n, err := w.file.Write(p[written : written+size])
		written += n

		if err != nil {
			return written, err
		}
	}

	return written, nil
}

func TestSparseWriterSplitWrites(t *testing.T) {
	src := sparseTestData(t)
	path := filepath.Join(t.TempDir(), "sparse")

	f, err := os.Create(path)
	require.NoError(t, err)

	t.Cleanup(func() { _ = f.Close() })

	writer := &sparseWriter{file: &splitWriteSeeker{file: f}}
	_, err = io.Copy(writer, bytes.NewReader(src))
	require.NoError(t, err)

	// A trailing hole is only reflected in the file size by truncation.
	err = f.Truncate(int64(len(src)))
	require.NoError(t, err)

	err = f.Close()
	require.NoError(t, err)

	dst, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, src, dst)

	requireSparse(t, path, int64(len(src)))
}

func newTestSFTPClient(t *testing.T, dir string) *sftp.Client {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })

	server, err := sftp.NewServer(serverConn, sftp.WithServerWorkingDirectory(dir))
	require.NoError(t, err)

	go func() {
		_ = server.Serve()
	}()

	client, err := sftp.NewClientPipe(clientConn, clientConn)
	require.NoError(t, err)

	t.Cleanup(func() { _ = client.Close() })

	return client
}

func TestCopySparseSFTP(t *testing.T) {
	src := sparseTestData(t)
	dir := t.TempDir()
	client := newTestSFTPClient(t, dir)

	f, err := client.OpenFile("sparse", os.O_CREATE|os.O_RDWR|os.O_TRUNC)
	require.NoError(t, err)

	_, err = f.Seek(0, io.SeekEnd)
	require.NoError(t, err)

	written, err := CopySparse(f, bytes.NewReader(src))
	require.NoError(t, err)
	require.EqualValues(t, len(src), written)

	err = f.Close()
	require.NoError(t, err)

	path := filepath.Join(dir, "sparse")

	dst, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, src, dst)

	requireSparse(t, path, int64(len(src)))
}

func TestCopySparseSFTPExistingData(t *testing.T) {
	dir := t.TempDir()
	client := newTestSFTPClient(t, dir)

	existing := bytes.Repeat([]byte{'A'}, 256*1024)

	f, err := client.OpenFile("existing", os.O_CREATE|os.O_RDWR|os.O_TRUNC)
	require.NoError(t, err)

	_, err = f.Write(existing)
	require.NoError(t, err)

	err = f.Close()
	require.NoError(t, err)

	f, err = client.OpenFile("existing", os.O_RDWR)
	require.NoError(t, err)

	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)

	src := make([]byte, len(existing)/2)
	copy(src, "start")
	copy(src[len(src)-5:], "end")

	written, err := CopySparse(f, bytes.NewReader(src))
	require.NoError(t, err)
	require.EqualValues(t, len(src), written)

	err = f.Close()
	require.NoError(t, err)

	dst, err := os.ReadFile(filepath.Join(dir, "existing"))
	require.NoError(t, err)

	expected := append([]byte{}, existing...)
	copy(expected, src)
	require.Equal(t, expected, dst)
}
