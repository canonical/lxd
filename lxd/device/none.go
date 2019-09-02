package device

type none struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *none) validateConfig() error {
	rules := map[string]func(string) error{} // No fields allowed.
	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	return nil
}

// Start is run when the device is added to the container.
func (d *none) Start() (*RunConfig, error) {
	return nil, nil
}

// Stop is run when the device is removed from the instance.
func (d *none) Stop() (*RunConfig, error) {
	return nil, nil
}
