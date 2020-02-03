package drivers

// Location is used to determine whether a rule should be appended or prepended
type Location int

// Values for Location
const (
	LocationPrepend = iota
	LocationAppend
)

// Family is used to change behavior based on which networking family is being referrenced
type Family string

// Values for Family
const (
	FamilyIPv4 = "ipv4"
	FamilyIPv6 = "ipv6"
)

// Table is used to define which table a new rule should be added to
type Table string

// Values for Table
const (
	TableAll    = ""
	TableNat    = "nat"
	TableFilter = "filter"
	TableMangle = "mangle"
)

// Action is used in NetworkSetupAllowForwarding()
type Action int

// Value for Action
const (
	ActionAccept = iota
	ActionReject
	ActionDrop
)
