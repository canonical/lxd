package fsmonitor

// Event is a numeric code identifying the event.
type Event int

const (
	// EventAdd represents the add event.
	EventAdd Event = iota

	// EventRemove represents the remove event.
	EventRemove

	// EventWrite represents the write event (a file that was opened for writing was closed).
	EventWrite

	// EventRename represents the rename event. This event will fire when a file is renamed to the watched file name,
	// e.g. If watching `/some-dir`, and a file is renamed from `/some-dir/file.txt.tmp` to `/some-dir/file.txt`, the
	// event fires for `/some-dir/file.txt`, and not for `/some-dir/file.txt.tmp`.
	EventRename
)

func (e Event) String() string {
	return map[Event]string{
		EventAdd:    "add",
		EventRemove: "remove",
		EventWrite:  "write",
		EventRename: "rename",
	}[e]
}
