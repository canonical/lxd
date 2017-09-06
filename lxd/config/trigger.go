package config

// Trigger can be used to trigger a custom function whenever a certain
// configuration key changes in a Map.
type Trigger struct {
	Key  string             // Name of the key that should activate this trigger.
	Func func(string) error // Function to trigger when the config value changes.
}
