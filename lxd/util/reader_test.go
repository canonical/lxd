package util

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestMaxBytesReaderWithinLimit(t *testing.T) {
	reader := MaxBytesReader(bytes.NewBufferString("hello"), 5)
	buf := make([]byte, 8)

	n, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("Expected nil error on first read, got %v", err)
	}

	if n != 5 {
		t.Fatalf("Expected to read 5 bytes, got %d", n)
	}

	if string(buf[:n]) != "hello" {
		t.Fatalf("Expected %q, got %q", "hello", string(buf[:n]))
	}

	n, err = reader.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Expected EOF on second read, got %v", err)
	}

	if n != 0 {
		t.Fatalf("Expected to read 0 bytes after EOF, got %d", n)
	}
}

func TestMaxBytesReaderExceedsLimit(t *testing.T) {
	reader := MaxBytesReader(bytes.NewBufferString("hello"), 4)
	buf := make([]byte, 8)

	n, err := reader.Read(buf)
	if n != 4 {
		t.Fatalf("Expected to read 4 bytes before limit error, got %d", n)
	}

	if string(buf[:n]) != "hell" {
		t.Fatalf("Expected %q, got %q", "hell", string(buf[:n]))
	}

	var maxErr *MaxBytesError
	if !errors.As(err, &maxErr) {
		t.Fatalf("Expected MaxBytesError, got %v", err)
	}

	if maxErr.Limit != 4 {
		t.Fatalf("Expected limit 4 in error, got %d", maxErr.Limit)
	}

	n, err = reader.Read(buf)
	if n != 0 {
		t.Fatalf("Expected sticky error read to return 0 bytes, got %d", n)
	}

	if !errors.As(err, &maxErr) {
		t.Fatalf("Expected sticky MaxBytesError, got %v", err)
	}
}

func TestMaxBytesReaderNegativeLimitTreatedAsZero(t *testing.T) {
	reader := MaxBytesReader(bytes.NewBufferString("hello"), -1)
	buf := make([]byte, 8)

	n, err := reader.Read(buf)
	if n != 0 {
		t.Fatalf("Expected to read 0 bytes when limit is negative, got %d", n)
	}

	var maxErr *MaxBytesError
	if !errors.As(err, &maxErr) {
		t.Fatalf("Expected MaxBytesError, got %v", err)
	}

	if maxErr.Limit != 0 {
		t.Fatalf("Expected error limit 0, got %d", maxErr.Limit)
	}
}

func TestMaxBytesReaderZeroLengthReadDoesNotConsume(t *testing.T) {
	reader := MaxBytesReader(bytes.NewBufferString("hello"), 5)

	n, err := reader.Read(nil)
	if err != nil {
		t.Fatalf("Expected nil error for zero-length read, got %v", err)
	}

	if n != 0 {
		t.Fatalf("Expected zero-length read to return 0, got %d", n)
	}

	buf := make([]byte, 8)
	n, err = reader.Read(buf)
	if err != nil {
		t.Fatalf("Expected nil error after zero-length read, got %v", err)
	}

	if n != 5 {
		t.Fatalf("Expected to read full payload after zero-length read, got %d", n)
	}

	if string(buf[:n]) != "hello" {
		t.Fatalf("Expected %q, got %q", "hello", string(buf[:n]))
	}
}

func TestMaxBytesReaderUnderlyingErrorSticky(t *testing.T) {
	sentinelErr := errors.New("sentinel")
	reader := MaxBytesReader(&errorReader{err: sentinelErr}, 10)

	buf := make([]byte, 8)
	n, err := reader.Read(buf)
	if n != 0 {
		t.Fatalf("Expected read count 0, got %d", n)
	}

	if !errors.Is(err, sentinelErr) {
		t.Fatalf("Expected sentinel error, got %v", err)
	}

	n, err = reader.Read(buf)
	if n != 0 {
		t.Fatalf("Expected sticky read count 0, got %d", n)
	}

	if !errors.Is(err, sentinelErr) {
		t.Fatalf("Expected sticky sentinel error, got %v", err)
	}
}

type errorReader struct {
	err error
}

func (r *errorReader) Read(_ []byte) (int, error) {
	return 0, r.err
}
