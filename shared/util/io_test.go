package util

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSafeCopyEmpty(t *testing.T) {
	dst := &bytes.Buffer{}
	n, err := SafeCopy(dst, strings.NewReader(""))
	if err != nil {
		t.Fatalf("Expected no error for empty reader, got %v", err)
	}

	if n != 0 {
		t.Fatalf("Expected 0 bytes written, got %d", n)
	}

	if dst.Len() != 0 {
		t.Fatalf("Expected empty destination, got %d bytes", dst.Len())
	}
}

func TestSafeCopySmallPayload(t *testing.T) {
	payload := "hello, world"
	dst := &bytes.Buffer{}

	n, err := SafeCopy(dst, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if n != int64(len(payload)) {
		t.Fatalf("Expected %d bytes written, got %d", len(payload), n)
	}

	if dst.String() != payload {
		t.Fatalf("Expected %q, got %q", payload, dst.String())
	}
}

func TestSafeCopyExactlyOneChunk(t *testing.T) {
	const chunkSize = 4 * 1024 * 1024
	payload := bytes.Repeat([]byte("x"), chunkSize)
	dst := &bytes.Buffer{}

	n, err := SafeCopy(dst, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Expected no error for exactly one chunk, got %v", err)
	}

	if n != int64(chunkSize) {
		t.Fatalf("Expected %d bytes written, got %d", chunkSize, n)
	}

	if !bytes.Equal(dst.Bytes(), payload) {
		t.Fatalf("Destination content does not match source")
	}
}

func TestSafeCopyMultipleChunks(t *testing.T) {
	const chunkSize = 4 * 1024 * 1024
	// 2.5 chunks to exercise the partial-last-chunk path.
	size := int(2.5 * chunkSize)
	payload := bytes.Repeat([]byte("a"), size)
	dst := &bytes.Buffer{}

	n, err := SafeCopy(dst, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Expected no error for multi-chunk copy, got %v", err)
	}

	if n != int64(size) {
		t.Fatalf("Expected %d bytes written, got %d", size, n)
	}

	if !bytes.Equal(dst.Bytes(), payload) {
		t.Fatalf("Destination content does not match source")
	}
}

func TestSafeCopyReturnsWrittenBytesOnError(t *testing.T) {
	sentinelErr := errors.New("write error")

	// Writer that accepts exactly one chunk-sized write then errors on the next.
	const chunkSize = 4 * 1024 * 1024
	dst := &errorWriter{limit: chunkSize, err: sentinelErr}

	// Two chunks so the second write will hit the error.
	payload := bytes.Repeat([]byte("b"), chunkSize*2)

	n, err := SafeCopy(dst, bytes.NewReader(payload))
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("Expected sentinel error, got %v", err)
	}

	if n != int64(chunkSize) {
		t.Fatalf("Expected %d bytes written before error, got %d", chunkSize, n)
	}
}

func TestSafeCopyPropagatesReaderError(t *testing.T) {
	sentinelErr := errors.New("read error")
	dst := &bytes.Buffer{}

	n, err := SafeCopy(dst, &errorReader{err: sentinelErr})
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("Expected sentinel error, got %v", err)
	}

	if n != 0 {
		t.Fatalf("Expected 0 bytes written on read error, got %d", n)
	}
}

// errorWriter accepts up to limit bytes total then returns err on subsequent writes.
type errorWriter struct {
	limit   int
	written int
	err     error
}

func (w *errorWriter) Write(p []byte) (int, error) {
	if w.written >= w.limit {
		return 0, w.err
	}

	w.written += len(p)
	return len(p), nil
}

// errorReader always returns the given error immediately.
type errorReader struct {
	err error
}

func (r *errorReader) Read(_ []byte) (int, error) {
	return 0, r.err
}
