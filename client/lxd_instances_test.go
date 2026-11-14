package lxd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// newTestProtocolLXD returns a ProtocolLXD talking to a test server running the given handler.
func newTestProtocolLXD(t *testing.T, handler http.HandlerFunc) *ProtocolLXD {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Failed parsing the test server URL: %v", err)
	}

	return &ProtocolLXD{
		ctx:         context.Background(),
		http:        server.Client(),
		httpBaseURL: *serverURL,
	}
}

func Test_consumeInstanceExecOutputLogFile(t *testing.T) {
	const logFilePath = "/1.0/instances/c1/logs/exec-output/exec_c1.stdout"

	requests := []string{}
	r := newTestProtocolLXD(t, func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, req.Method+" "+req.URL.Path)

		switch req.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte("some output"))
		case http.MethodDelete:
			_, _ = w.Write([]byte(`{"type":"sync","status":"Success","status_code":200}`))
		}
	})

	buf := &bytes.Buffer{}

	err := r.consumeInstanceExecOutputLogFile("c1", logFilePath, buf)
	if err != nil {
		t.Fatalf("consumeInstanceExecOutputLogFile() returned an error: %v", err)
	}

	if buf.String() != "some output" {
		t.Errorf("consumeInstanceExecOutputLogFile() copied %q, want %q", buf.String(), "some output")
	}

	// The log file is fetched and deleted exactly once.
	want := []string{"GET " + logFilePath, "DELETE " + logFilePath}
	if len(requests) != len(want) {
		t.Fatalf("consumeInstanceExecOutputLogFile() sent %v, want %v", requests, want)
	}

	for i, request := range requests {
		if request != want[i] {
			t.Errorf("consumeInstanceExecOutputLogFile() sent %v, want %v", requests, want)
			break
		}
	}
}

func Test_consumeInstanceExecOutputLogFileWithoutWriter(t *testing.T) {
	const logFilePath = "/1.0/instances/c1/logs/exec-output/exec_c1.stderr"

	requests := []string{}
	r := newTestProtocolLXD(t, func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, req.Method+" "+req.URL.Path)
		_, _ = w.Write([]byte(`{"type":"sync","status":"Success","status_code":200}`))
	})

	err := r.consumeInstanceExecOutputLogFile("c1", logFilePath, nil)
	if err != nil {
		t.Fatalf("consumeInstanceExecOutputLogFile() returned an error: %v", err)
	}

	// Without a writer the content is not fetched, but the log file is still deleted.
	if len(requests) != 1 || requests[0] != "DELETE "+logFilePath {
		t.Errorf("consumeInstanceExecOutputLogFile() sent %v, want %v", requests, []string{"DELETE " + logFilePath})
	}
}

func Test_consumeInstanceExecOutputLogFileGetError(t *testing.T) {
	r := newTestProtocolLXD(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"type":"error","error":"Failed getting the log file","error_code":500}`))
			return
		}

		_, _ = w.Write([]byte(`{"type":"sync","status":"Success","status_code":200}`))
	})

	err := r.consumeInstanceExecOutputLogFile("c1", "/1.0/instances/c1/logs/exec-output/exec_c1.stdout", &bytes.Buffer{})
	if err == nil {
		t.Error("consumeInstanceExecOutputLogFile() did not return an error when the log file could not be fetched")
	}
}
