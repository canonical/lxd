package api

// SelectorMatcher contains the properties and values that a Selector selects against.
//
// swagger:model
//
// API extension: instance_placement_rules.
type SelectorMatcher struct {
	// Property is the property of the entity to perform the match against.
	Property string `json:"property" yaml:"property"`

	// Values are a list of values to find in the property.
	Values []string `json:"values" yaml:"values"`
}

// Selector represents a method of selecting one or more entities of a particular type.
//
// swagger:model
//
// API extension: instance_placement_rules.
type Selector struct {
	// EntityType is the type of entity to perform the selection against.
	EntityType string `json:"entity_type" yaml:"entity_type"`

	// Matchers are a list of matchers to use when performing the selection.
	Matchers []SelectorMatcher `json:"matchers" yaml:"matchers"`
}
