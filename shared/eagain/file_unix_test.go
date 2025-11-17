//go:build linux

package eagain

import (
	"errors"
	"io"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// mockReader is a mock [io.Reader] that can simulate EAGAIN and EINTR errors.
type mockReader struct {
	data      []byte
	readCount int
	errorSeq  []error
	callCount int
}

// Read implements [io.Reader] and simulates read operations with configurable error sequences.
// It returns errors from errorSeq in order, then returns data once errors are exhausted.
func (m *mockReader) Read(p []byte) (int, error) {
	if m.callCount < len(m.errorSeq) {
		err := m.errorSeq[m.callCount]
		m.callCount++
		if err != nil {
			return 0, err
		}
	}

	if m.readCount >= len(m.data) {
		return 0, io.EOF
	}

	n := copy(p, m.data[m.readCount:])
	m.readCount += n
	m.callCount++
	return n, nil
}

// mockWriter is a mock [io.Writer] that can simulate EAGAIN and EINTR errors.
type mockWriter struct {
	data      []byte
	errorSeq  []error
	callCount int
}

// Write implements [io.Writer] and simulates write operations with configurable error sequences.
// It returns errors from errorSeq in order, then appends data once errors are exhausted.
func (m *mockWriter) Write(p []byte) (int, error) {
	if m.callCount < len(m.errorSeq) {
		err := m.errorSeq[m.callCount]
		m.callCount++
		if err != nil {
			return 0, err
		}
	}

	m.data = append(m.data, p...)
	m.callCount++
	return len(p), nil
}

func TestReader_Read_Success(t *testing.T) {
	data := []byte("test data")
	mock := &mockReader{data: data}

	reader := Reader{Reader: mock}
	buf := make([]byte, len(data))
	n, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Expected %d bytes read, got: %d", len(data), n)
	}

	if string(buf) != string(data) {
		t.Fatalf("Expected data %q, got: %q", string(data), string(buf))
	}
}

func TestReader_Read_EOF(t *testing.T) {
	mock := &mockReader{data: []byte{}}
	reader := Reader{Reader: mock}

	buf := make([]byte, 10)
	_, err := reader.Read(buf)
	if err != io.EOF {
		t.Fatalf("Expected io.EOF, got: %v", err)
	}
}

func TestReader_Read_RetryOnEAGAIN(t *testing.T) {
	data := []byte("retry test")

	// Create a SyscallError wrapping EAGAIN
	eagainErr := &os.SyscallError{
		Syscall: "read",
		Err:     unix.EAGAIN,
	}

	mock := &mockReader{
		data:     data,
		errorSeq: []error{eagainErr, eagainErr, nil},
	}

	reader := Reader{Reader: mock}
	buf := make([]byte, len(data))
	n, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("Expected no error after retries, got: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Expected %d bytes read, got: %d", len(data), n)
	}

	if string(buf) != string(data) {
		t.Fatalf("Expected data %q, got: %q", string(data), string(buf))
	}

	if mock.callCount != 4 {
		t.Fatalf("Expected 4 read attempts (2 EAGAIN errors + 1 nil from errorSeq + 1 success), got: %d", mock.callCount)
	}
}

func TestReader_Read_RetryOnEINTR(t *testing.T) {
	data := []byte("interrupt test")

	// Create a SyscallError wrapping EINTR
	eintrErr := &os.SyscallError{
		Syscall: "read",
		Err:     unix.EINTR,
	}

	mock := &mockReader{
		data:     data,
		errorSeq: []error{eintrErr, nil},
	}

	reader := Reader{Reader: mock}
	buf := make([]byte, len(data))
	n, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("Expected no error after retries, got: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Expected %d bytes read, got: %d", len(data), n)
	}

	if mock.callCount != 3 {
		t.Fatalf("Expected 3 read attempts (1 EINTR error + 1 nil from errorSeq + 1 success), got: %d", mock.callCount)
	}
}

func TestReader_Read_OtherError(t *testing.T) {
	expectedErr := errors.New("some other error")

	mock := &mockReader{
		errorSeq: []error{expectedErr},
	}

	reader := Reader{Reader: mock}
	buf := make([]byte, 10)
	_, err := reader.Read(buf)
	if err != expectedErr {
		t.Fatalf("Expected error %v, got: %v", expectedErr, err)
	}

	if mock.callCount != 1 {
		t.Fatalf("Expected 1 read attempt, got: %d", mock.callCount)
	}
}

func TestReader_Read_PathError(t *testing.T) {
	data := []byte("path error test")

	// Create a PathError wrapping EAGAIN
	pathErr := &os.PathError{
		Op:   "read",
		Path: "/dev/null",
		Err:  unix.EAGAIN,
	}

	mock := &mockReader{
		data:     data,
		errorSeq: []error{pathErr, nil},
	}

	reader := Reader{Reader: mock}
	buf := make([]byte, len(data))
	n, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("Expected no error after retries, got: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Expected %d bytes read, got: %d", len(data), n)
	}
}

func TestReader_Read_DirectErrno(t *testing.T) {
	data := []byte("direct errno test")

	mock := &mockReader{
		data:     data,
		errorSeq: []error{unix.EAGAIN, nil},
	}

	reader := Reader{Reader: mock}
	buf := make([]byte, len(data))
	n, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("Expected no error after retries, got: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Expected %d bytes read, got: %d", len(data), n)
	}
}

func TestWriter_Write_Success(t *testing.T) {
	data := []byte("write test")
	mock := &mockWriter{}

	writer := Writer{Writer: mock}
	n, err := writer.Write(data)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Expected %d bytes written, got: %d", len(data), n)
	}

	if string(mock.data) != string(data) {
		t.Fatalf("Expected data %q, got: %q", string(data), string(mock.data))
	}
}

func TestWriter_Write_RetryOnEAGAIN(t *testing.T) {
	data := []byte("write retry test")

	eagainErr := &os.SyscallError{
		Syscall: "write",
		Err:     unix.EAGAIN,
	}

	mock := &mockWriter{
		errorSeq: []error{eagainErr, eagainErr, nil},
	}

	writer := Writer{Writer: mock}
	n, err := writer.Write(data)
	if err != nil {
		t.Fatalf("Expected no error after retries, got: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Expected %d bytes written, got: %d", len(data), n)
	}

	if string(mock.data) != string(data) {
		t.Fatalf("Expected data %q, got: %q", string(data), string(mock.data))
	}

	if mock.callCount != 4 {
		t.Fatalf("Expected 4 write attempts (2 EAGAIN errors + 1 nil from errorSeq + 1 success), got: %d", mock.callCount)
	}
}

func TestWriter_Write_RetryOnEINTR(t *testing.T) {
	data := []byte("write interrupt test")

	eintrErr := &os.SyscallError{
		Syscall: "write",
		Err:     unix.EINTR,
	}

	mock := &mockWriter{
		errorSeq: []error{eintrErr, eintrErr, eintrErr, nil},
	}

	writer := Writer{Writer: mock}
	n, err := writer.Write(data)
	if err != nil {
		t.Fatalf("Expected no error after retries, got: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Expected %d bytes written, got: %d", len(data), n)
	}

	if mock.callCount != 5 {
		t.Fatalf("Expected 5 write attempts (3 EINTR errors + 1 nil from errorSeq + 1 success), got: %d", mock.callCount)
	}
}

func TestWriter_Write_OtherError(t *testing.T) {
	expectedErr := errors.New("write failed")
	mock := &mockWriter{
		errorSeq: []error{expectedErr},
	}

	writer := Writer{Writer: mock}
	_, err := writer.Write([]byte("test"))
	if err != expectedErr {
		t.Fatalf("Expected error %v, got: %v", expectedErr, err)
	}

	if mock.callCount != 1 {
		t.Fatalf("Expected 1 write attempt, got: %d", mock.callCount)
	}
}

func TestWriter_Write_PathError(t *testing.T) {
	data := []byte("write path error test")

	pathErr := &os.PathError{
		Op:   "write",
		Path: "/dev/null",
		Err:  unix.EINTR,
	}

	mock := &mockWriter{
		errorSeq: []error{pathErr, nil},
	}

	writer := Writer{Writer: mock}
	n, err := writer.Write(data)
	if err != nil {
		t.Fatalf("Expected no error after retries, got: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Expected %d bytes written, got: %d", len(data), n)
	}
}

func TestWriter_Write_DirectErrno(t *testing.T) {
	data := []byte("write direct errno test")

	mock := &mockWriter{
		errorSeq: []error{unix.EINTR, unix.EAGAIN, nil},
	}

	writer := Writer{Writer: mock}
	n, err := writer.Write(data)
	if err != nil {
		t.Fatalf("Expected no error after retries, got: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Expected %d bytes written, got: %d", len(data), n)
	}

	if mock.callCount != 4 {
		t.Fatalf("Expected 4 write attempts (2 errno errors + 1 nil from errorSeq + 1 success), got: %d", mock.callCount)
	}
}
