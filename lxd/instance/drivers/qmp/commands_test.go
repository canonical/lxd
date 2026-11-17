package qmp

import (
	"encoding/json"
	"net"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
)

// recordCommands starts a mock QMP server which acknowledges every command.
// It returns the commands received while run was executing.
func recordCommands(t *testing.T, run func(m *Monitor) error) []qmpCommand {
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

			// Ignore the handshake and the background serial ring buffer polling.
			if cmd.Execute != "qmp_capabilities" && cmd.Execute != "ringbuf-read" {
				mu.Lock()
				got = append(got, cmd)
				mu.Unlock()
			}

			err = enc.Encode(qmpResponse{ID: cmd.ID})
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

	err = run(m)
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	return slices.Clone(got)
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
