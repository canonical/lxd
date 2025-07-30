package query

import (
	"database/sql/driver"
	"errors"
	"fmt"
)

// TextScanner should be implemented by non-string types when used to represent a TEXT column.
type TextScanner interface {
	ScanText(str string) error
}

// IntegerScanner should be implemented by non-int types when used to represent an INTEGER column.
type IntegerScanner interface {
	ScanInteger(i int64) error
}

// ScanValue scans the value into the target based on the interfaces implemented by the target. For example, if the
// target implements TextScanner, then ScanValue expects the value to be a string.
func ScanValue(value any, target any, allowNull bool) error {
	if value == nil {
		if allowNull {
			return nil
		}

		return errors.New("Cannot scan null value")
	}

	switch scanner := target.(type) {
	case TextScanner:
		driverVal, err := driver.String.ConvertValue(value)
		if err != nil {
			return fmt.Errorf("Invalid text column: %w", err)
		}

		str, ok := driverVal.(string)
		if !ok {
			return fmt.Errorf("Expected string, got `%v` (%T)", driverVal, driverVal)
		}

		return scanner.ScanText(str)
	case IntegerScanner:
		driverVal, err := driver.Int32.ConvertValue(value)
		if err != nil {
			return fmt.Errorf("Invalid integer column: %w", err)
		}

		i, ok := driverVal.(int64)
		if !ok {
			return fmt.Errorf("Expected int64, got `%v` (%T)", driverVal, driverVal)
		}

		return scanner.ScanInteger(i)
	}

	return fmt.Errorf("Cannot scan %v (type %T), target %v (type %T) does not implement any scanner interfaces", value, value, target, target)
}
