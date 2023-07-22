package drivers

// Event is a numeric code identifying the event.
type Event int

const (
	// Add represents the add event.
	Add Event = iota
	// Remove represents the remove event.
	Remove
)

// Converts event type enum to its corresponding string representation.
func (e Event) String() string {
	return map[Event]string{
		Add:    "add",
		Remove: "remove",
	}[e]
}
