package protocol

import (
	"reflect"
	"strings"
	"unsafe"

	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
	"github.com/golang/protobuf/proto"
	"github.com/pkg/errors"
)

// MarshalCommand marshals a dqlite FSM command.
func MarshalCommand(command *Command) ([]byte, error) {
	return proto.Marshal(command)
}

// UnmarshalCommand unmarshals a dqlite FSM command.
func UnmarshalCommand(data []byte) (*Command, error) {
	command := &Command{}
	if err := proto.Unmarshal(data, command); err != nil {
		return nil, errors.Wrap(err, "protobuf failure")
	}
	return command, nil
}

// NewOpen returns a new Command with Open parameters.
func NewOpen(name string) *Command {
	params := &Command_Open{Open: &Open{Name: name}}
	return newCommand(params)
}

// NewBegin returns a new Command with Begin parameters.
func NewBegin(txid uint64, name string) *Command {
	params := &Command_Begin{Begin: &Begin{Txid: txid, Name: name}}
	return newCommand(params)
}

// NewFrames returns a new WalFrames protobuf message.
func NewFrames(txid uint64, filename string, list bindings.WalReplicationFrameList) *Command {
	length := list.Len()
	pageSize := list.PageSize()

	numbers := make([]uint32, length)
	pages := make([]byte, length*pageSize)

	for i := range numbers {
		data, pgno, _ := list.Frame(i)
		numbers[i] = uint32(pgno)
		header := reflect.SliceHeader{Data: uintptr(data), Len: pageSize, Cap: pageSize}
		var slice []byte
		slice = reflect.NewAt(reflect.TypeOf(slice), unsafe.Pointer(&header)).Elem().Interface().([]byte)
		copy(pages[i*pageSize:(i+1)*pageSize], slice)
	}

	isCommit := int32(0)
	if list.IsCommit() {
		isCommit = int32(1)
	}

	params := &Command_Frames{Frames: &Frames{
		Txid:        txid,
		PageSize:    int32(pageSize),
		PageNumbers: numbers,
		PageData:    pages,
		Truncate:    uint32(list.Truncate()),
		IsCommit:    isCommit,
		Filename:    filename,
	}}

	return newCommand(params)
}

// NewUndo returns a new Undo protobuf message.
func NewUndo(txid uint64) *Command {
	params := &Command_Undo{Undo: &Undo{
		Txid: txid,
	}}
	return newCommand(params)
}

// NewEnd returns a new End protobuf message.
func NewEnd(txid uint64) *Command {
	params := &Command_End{End: &End{
		Txid: txid,
	}}
	return newCommand(params)
}

// NewCheckpoint returns a new Checkpoint protobuf message.
func NewCheckpoint(name string) *Command {
	params := &Command_Checkpoint{Checkpoint: &Checkpoint{
		Name: name,
	}}
	return newCommand(params)
}

func newCommand(payload isCommand_Payload) *Command {
	return &Command{Payload: payload}
}

// Name returns a human readable name for the command, based on its Params
// type.
func (c *Command) Name() string {
	typeName := reflect.TypeOf(c.Payload).Elem().String()
	return strings.ToLower(strings.Replace(typeName, "protocol.Command_", "", 1))
}
