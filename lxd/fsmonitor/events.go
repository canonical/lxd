package fsmonitor

// Event is a numeric code identifying the event.
type Event int

const (
	// EventAdd represents the add event.
	EventAdd Event = iota

	// EventRemove represents the remove event.
	EventRemove
)

func (e Event) String() string {
	return map[Event]string{
		EventAdd:    "add",
		EventRemove: "remove",
	}[e]
}
