package shared

import (
	"bytes"
	"errors"
	"io"
	"os"
)

// sparseBlockSize is the granularity at which zero-filled data is turned into holes.
const sparseBlockSize = 4096

var sparseZeroBlock = make([]byte, sparseBlockSize)

// SparseFile is a file that supports the operations needed to write sparse data.
type SparseFile interface {
	io.WriteSeeker
	Stat() (os.FileInfo, error)
	Truncate(size int64) error
}

// CopySparse copies src into dst starting at its current offset.
//
// Runs of zero-filled blocks are skipped rather than written, leaving holes in
// the destination file. If the destination already contains data beyond its
// current offset, the data is copied without sparse handling to avoid leaving
// stale bytes in place of skipped blocks; data beyond the copied range is left
// untouched. If the source ends in a hole, dst is extended to the final size.
// It returns the number of bytes read from src.
func CopySparse(dst SparseFile, src io.Reader) (int64, error) {
	offset, err := dst.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}

	info, err := dst.Stat()
	if err != nil {
		return 0, err
	}

	var writer *sparseWriter
	var written int64

	// Seeking over a block does not clear existing data, so only use the
	// sparse path when everything past the current offset is new.
	if info.Size() > offset {
		written, err = io.Copy(dst, src)
	} else {
		writer = &sparseWriter{
			file:        dst,
			offset:      offset,
			lastDataEnd: offset,
		}

		written, err = io.Copy(writer, newAlignedReader(src, offset))
	}

	if err != nil {
		return written, err
	}

	// A file ending in a hole needs to be extended explicitly, otherwise the
	// last write already set the file size.
	if writer != nil && writer.lastDataEnd < offset+written {
		err = dst.Truncate(offset + written)
		if err != nil {
			return written, err
		}
	}

	return written, nil
}

// alignedReader reads data in chunks aligned to sparseBlockSize relative to the
// initial file offset. Reads fill the given buffer so that sparseWriter always
// sees whole blocks.
type alignedReader struct {
	reader io.Reader
	first  int
}

func newAlignedReader(reader io.Reader, offset int64) *alignedReader {
	first := 0
	remainder := offset % sparseBlockSize
	if remainder != 0 {
		first = int(sparseBlockSize - remainder)
	}

	return &alignedReader{
		reader: reader,
		first:  first,
	}
}

// Read implements io.Reader.
func (r *alignedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	if r.first > 0 {
		if len(p) > r.first {
			p = p[:r.first]
		}

		n, err := io.ReadFull(r.reader, p)
		r.first -= n

		return n, readError(err)
	}

	n, err := io.ReadFull(r.reader, p)

	return n, readError(err)
}

// readError converts io.ReadFull's partial-read error into io.EOF, which is
// what io.Copy expects from a reader that has reached the end.
func readError(err error) error {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return io.EOF
	}

	return err
}

// sparseWriter writes data while leaving holes in place of zero-filled blocks.
type sparseWriter struct {
	file        io.WriteSeeker
	offset      int64
	lastDataEnd int64
}

// Write writes p to the underlying file, skipping runs of zero-filled blocks.
func (w *sparseWriter) Write(p []byte) (int, error) {
	written := 0

	for written < len(p) {
		zero := sparseIsZero(p[written:])
		end := sparseRunEnd(p, written, zero)

		if zero {
			_, err := w.file.Seek(int64(end-written), io.SeekCurrent)
			if err != nil {
				return written, err
			}

			w.offset += int64(end - written)
			written = end
			continue
		}

		n, err := w.file.Write(p[written:end])
		w.offset += int64(n)
		w.lastDataEnd = w.offset
		written += n

		if err != nil {
			return written, err
		}
	}

	return written, nil
}

// sparseIsZero checks whether the first block of p is zero-filled.
func sparseIsZero(p []byte) bool {
	size := min(len(p), sparseBlockSize)

	return bytes.Equal(p[:size], sparseZeroBlock[:size])
}

// sparseRunEnd returns the end of the run of blocks starting at offset that match zero.
func sparseRunEnd(p []byte, offset int, zero bool) int {
	end := offset
	for end < len(p) && sparseIsZero(p[end:]) == zero {
		end += sparseBlockSize
	}

	return min(end, len(p))
}
