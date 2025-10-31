package qmp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared/logger"
)

type qemuMachineProtocol struct {
	oobSupported bool            // Out of band support or not
	c            net.Conn        // Underlying connection
	uc           *net.UnixConn   // Underlying unix socket connection
	mu           sync.Mutex      // Serialize running command
	replies      sync.Map        // Replies channels
	events       <-chan qmpEvent // Events channel
	listeners    atomic.Uint32   // Listeners number
	cid          atomic.Uint32   // Auto increase command id
}

// qmpEvent represents a QEMU QMP event.
type qmpEvent struct {
	// Event name, e.g., BLOCK_JOB_COMPLETE
	Event string `json:"event"`

	// Arbitrary event data
	Data map[string]any `json:"data"`

	// Event timestamp, provided by QEMU.
	Timestamp *struct {
		Seconds      int64 `json:"seconds"`
		Microseconds int64 `json:"microseconds"`
	} `json:"timestamp"`
}

// qmpCommand represents a QMP command.
type qmpCommand struct {
	// Name of the command to run
	Execute string `json:"execute,omitempty"`

	// Name of the Out-of-band execution to run
	ExecuteOutOfBand string `json:"exec-oob,omitempty"`

	// Optional arguments for the above command.
	Arguments any `json:"arguments,omitempty"`

	// Optional id for transaction identification associated with the command
	// execution
	//
	// According QMP spec it should be any json value type. For LXD `uint32`
	// (skip zero) is good enough to identify transaction.
	ID uint32 `json:"id,omitempty"`
}

// qmpResponse represents a QMP response with id and return.
type qmpResponse struct {
	// Optional id for transaction identification associated with the response
	ID uint32 `json:"id,omitempty"`

	// Return response return
	Return any `json:"return,omitempty"`
}

// qmpError represents a QMP response error.
type qmpError struct {
	Class string `json:"class,omitempty"`
	Desc  string `json:"desc,omitempty"`
}

func (e *qmpError) Error() string {
	if e == nil {
		return ""
	}

	return fmt.Sprintf("%s: %s", e.Class, e.Desc)
}

// rawResponse represents QMP raw response with id, error and raw bytes.
type rawResponse struct {
	// Optional id for transaction identification associated with the response
	ID uint32 `json:"id"`

	// Error response error
	Error *qmpError `json:"error,omitempty"`

	raw []byte // raw data, json field ignored
	err error  // runtime error, json field ignored
}

// disconnect closes the QEMU monitor socket connection.
func (qmp *qemuMachineProtocol) disconnect() error {
	qmp.listeners.Store(0)
	return qmp.c.Close()
}

// qmpIncreaseID increase ID and skip zero.
func (qmp *qemuMachineProtocol) qmpIncreaseID() uint32 {
	id := qmp.cid.Add(1)
	if id == 0 {
		id = qmp.cid.Add(1)
	}

	return id
}

// connect sets up a QMP connection.
func (qmp *qemuMachineProtocol) connect() error {
	enc := json.NewEncoder(qmp.c)
	dec := json.NewDecoder(qmp.c)

	// Check for banner on startup
	ban := struct {
		QMP struct {
			Capabilities []string `json:"capabilities"`
		} `json:"QMP"`
	}{}

	err := dec.Decode(&ban)
	if err != nil {
		return err
	}

	qmp.oobSupported = slices.Contains(ban.QMP.Capabilities, "oob")

	// Issue capabilities handshake
	id := qmp.qmpIncreaseID()
	cmd := qmpCommand{Execute: "qmp_capabilities", ID: id}
	err = enc.Encode(cmd)
	if err != nil {
		return err
	}

	// Check for no error on return
	r := &rawResponse{}
	err = dec.Decode(r)
	if err != nil {
		return err
	}

	if r.Error != nil {
		return r.Error
	}

	if r.ID != id {
		return fmt.Errorf("reply id %d and command id %d mismatch", r.ID, id)
	}

	// Initialize listener for command responses and asynchronous events.
	events := make(chan qmpEvent, 128)
	go qmp.listen(qmp.c, events, &qmp.replies)
	qmp.events = events
	return nil
}

// getEvents streams QEMU QMP Events.
func (qmp *qemuMachineProtocol) getEvents(context.Context) (<-chan qmpEvent, error) {
	qmp.listeners.Add(1)
	return qmp.events, nil
}

func (qmp *qemuMachineProtocol) listen(r io.Reader, events chan<- qmpEvent, replies *sync.Map) {
	defer close(events)

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		var e qmpEvent

		b := scanner.Bytes()
		err := json.Unmarshal(b, &e)
		if err != nil {
			continue
		}

		// If data does not have an event type, it must be in response to a command.
		if e.Event == "" {
			r := rawResponse{}
			err = json.Unmarshal(b, &r)
			if err != nil {
				continue
			}

			key := r.ID
			if key == 0 {
				// Discard response without a request ID.
				continue
			}

			val, ok := replies.LoadAndDelete(key)
			if !ok {
				// Discard unexpected response.
				continue
			}

			reply, ok := val.(chan rawResponse)
			if !ok {
				// Skip bad messages.
				logger.Error("Failed to cast QMP reply to chan rawResponse")
				continue
			}

			r.raw = make([]byte, len(b))
			copy(r.raw, b)
			reply <- r

			continue
		}

		// If nobody is listening for events, do not bother sending them.
		if qmp.listeners.Load() == 0 {
			continue
		}

		select {
		case events <- e:
			logger.Debugf("Event dispatched: %s", b)
		default:
			logger.Debugf("Event discarded: %s", b)
		}
	}

	err := scanner.Err()
	if err != nil {
		errReply := make(chan rawResponse, 1)
		replies.Store(0, errReply)

		r := rawResponse{err: err}
		errReply <- r
	}
}

// run executes the given QAPI command against a domain's QEMU instance.
func (qmp *qemuMachineProtocol) run(command []byte) ([]byte, error) {
	// Just call RunWithFile with no file
	return qmp.runWithFile(command, nil)
}

func (qmp *qemuMachineProtocol) qmpWriteMsg(b []byte, file *os.File) error {
	if file == nil {
		// Just send a normal command through.
		_, err := qmp.c.Write(b)
		return err
	}

	if !qmp.oobSupported {
		return errors.New("The QEMU server doesn't support oob (needed for RunWithFile)")
	}

	// Send the command along with the file descriptor.
	oob := unix.UnixRights(int(file.Fd()))
	_, _, err := qmp.uc.WriteMsgUnix(b, oob, nil)
	if err != nil {
		return err
	}

	return nil
}

// runWithFile executes for passing a file through out-of-band data.
func (qmp *qemuMachineProtocol) runWithFile(command []byte, file *os.File) ([]byte, error) {
	// Only allow a single command to be run at a time to ensure that responses
	// to a command cannot be mixed with responses from another command
	qmp.mu.Lock()
	defer qmp.mu.Unlock()

	id := qmp.qmpIncreaseID()
	command, err := qmp.qmpInjectID(command, id)
	if err != nil {
		return nil, err
	}

	repCh := make(chan rawResponse, 1)
	qmp.replies.Store(id, repCh)

	err = qmp.qmpWriteMsg(command, file)
	if err != nil {
		qmp.replies.Delete(id)
		return nil, err
	}

	// Wait for a response or error to our command
	res := <-repCh
	if res.err != nil {
		return nil, res.err
	}

	if res.Error != nil {
		return nil, res.Error
	}

	return res.raw, nil
}

func (qmp *qemuMachineProtocol) qmpInjectID(command []byte, id uint32) ([]byte, error) {
	req := &qmpCommand{}
	err := json.Unmarshal(command, req)
	if err != nil {
		return nil, err
	}

	req.ID = id
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	return b, nil
}
