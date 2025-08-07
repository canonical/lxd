package identity

// typeInfoCommon is a common implementation of the [Type] interface.
type typeInfoCommon struct{}

// IsAdmin returns false by default.
func (typeInfoCommon) IsAdmin() bool {
	return false
}

// IsCacheable returns false by default.
func (typeInfoCommon) IsCacheable() bool {
	return false
}

// IsFineGrained returns false by default.
func (typeInfoCommon) IsFineGrained() bool {
	return false
}

// IsPending returns false by default.
func (typeInfoCommon) IsPending() bool {
	return false
}
