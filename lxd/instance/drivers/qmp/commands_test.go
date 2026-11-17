package qmp

import (
	"encoding/json"
	"net"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// recordCommands starts a mock QMP server which acknowledges every command.
// It returns the commands received while run was executing.
func recordCommands(t *testing.T, run func(m *Monitor) error) []qmpCommand {
	t.Helper()

	got, err := recordCommandsReply(t, nil, run)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// recordCommandsReply is like recordCommands but lets reply build the response to each command.
// A nil reply acknowledges every command. The error returned by run is returned to the caller.
func recordCommandsReply(t *testing.T, reply func(cmd qmpCommand) any, run func(m *Monitor) error) ([]qmpCommand, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "qmp.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = l.Close() })

	var mu sync.Mutex
	var got []qmpCommand
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}

		defer func() { _ = c.Close() }()

		enc := json.NewEncoder(c)
		dec := json.NewDecoder(c)
		_ = enc.Encode(testingGreeting)

		for {
			var cmd qmpCommand
			err = dec.Decode(&cmd)
			if err != nil {
				return
			}

			var resp any = qmpResponse{ID: cmd.ID}

			// Ignore the handshake and the background serial ring buffer polling.
			if cmd.Execute != "qmp_capabilities" && cmd.Execute != "ringbuf-read" {
				mu.Lock()
				got = append(got, cmd)
				mu.Unlock()

				if reply != nil {
					resp = reply(cmd)
				}
			}

			err = enc.Encode(resp)
			if err != nil {
				return
			}
		}
	}()

	m, err := Connect(path, "serial", func(string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(m.Disconnect)

	runErr := run(m)

	mu.Lock()
	defer mu.Unlock()

	return slices.Clone(got), runErr
}

func TestIOThreadErrors(t *testing.T) {
	tests := []struct {
		name    string
		desc    string
		run     func(m *Monitor) error
		wantErr string
	}{
		{"add fails", "duplicate ID 'iothread0'", func(m *Monitor) error { return m.AddIOThread("iothread0") }, "Failed adding iothread"},
		{"remove fails", "object 'iothread0' is in use", func(m *Monitor) error { return m.RemoveIOThread("iothread0") }, "Failed removing iothread"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reply := func(cmd qmpCommand) any {
				// Only fail the iothread commands, the monitor pings QEMU after an error to check that it is alive.
				if cmd.Execute != "object-add" && cmd.Execute != "object-del" {
					return qmpResponse{ID: cmd.ID}
				}

				return map[string]any{"id": cmd.ID, "error": map[string]string{"class": "GenericError", "desc": tt.desc}}
			}

			_, err := recordCommandsReply(t, reply, tt.run)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), tt.desc) {
				t.Fatalf("Unexpected error:\n- want: %q and %q\n-  got: %v", tt.wantErr, tt.desc, err)
			}
		})
	}
}

func TestIOThreadCommands(t *testing.T) {
	got := recordCommands(t, func(m *Monitor) error {
		err := m.AddIOThread("iothread0")
		if err != nil {
			return err
		}

		return m.RemoveIOThread("iothread0")
	})

	want := []struct {
		execute string
		args    map[string]any
	}{
		{"object-add", map[string]any{"qom-type": "iothread", "id": "iothread0"}},
		{"object-del", map[string]any{"id": "iothread0"}},
	}

	if len(got) != len(want) {
		t.Fatalf("Unexpected number of commands: want %d, got %d: %+v", len(want), len(got), got)
	}

	for i, w := range want {
		if got[i].Execute != w.execute {
			t.Errorf("Command %d: want %q, got %q", i, w.execute, got[i].Execute)
		}

		if !reflect.DeepEqual(got[i].Arguments, w.args) {
			t.Errorf("Command %d arguments:\n- want: %v\n-  got: %v", i, w.args, got[i].Arguments)
		}
	}
}
