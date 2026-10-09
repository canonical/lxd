package qmp

import (
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMonitorConcurrentStateAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qmp.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = l.Close() })

	release := make(chan struct{})
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- func() error {
			c, err := l.Accept()
			if err != nil {
				return err
			}

			defer func() { _ = c.Close() }()

			// Fail later dials fast once the monitor is gone, rather than waiting for a greeting.
			_ = l.Close()

			enc := json.NewEncoder(c)
			dec := json.NewDecoder(c)
			err = enc.Encode(testingGreeting)
			if err != nil {
				return err
			}

			var cmd qmpCommand
			err = dec.Decode(&cmd)
			if err != nil {
				return err
			}

			err = enc.Encode(qmpResponse{ID: cmd.ID})
			if err != nil {
				return err
			}

			<-release

			// Closing the connection afterwards makes the event goroutine disconnect the monitor.
			for range 10 {
				err = enc.Encode(qmpEvent{Event: EventVMShutdown})
				if err != nil {
					return err
				}
			}

			return nil
		}()
	}()

	handler := func(string, map[string]any) {}
	m, err := Connect(path, "serial", handler)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(m.Disconnect)

	chDisconnect, err := m.Wait()
	if err != nil {
		t.Fatal(err)
	}

	wg := sync.WaitGroup{}
	for range 4 {
		wg.Go(func() {
			for range 100 {
				m.SetOnDisconnectEvent(true)
				_, _ = m.Wait()
				_, _ = Connect(path, "serial", handler)
			}
		})
	}

	close(release)
	wg.Wait()

	select {
	case <-chDisconnect:
	case <-time.After(5 * time.Second):
		t.Fatal("monitor did not disconnect after the connection closed")
	}

	err = <-serverErr
	if err != nil {
		t.Fatal(err)
	}

	_, err = m.Wait()
	if !errors.Is(err, ErrMonitorDisconnect) {
		t.Fatalf("unexpected error:\n- want: %v\n-  got: %v", ErrMonitorDisconnect, err)
	}
}
