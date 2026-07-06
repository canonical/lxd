package units

import (
	"fmt"
	"strconv"
)

func handleOverflow(val int64, mult int64) (int64, error) {
	result := val * mult
	if val == 0 || mult == 0 || val == 1 || mult == 1 {
		return result, nil
	}

	if val != 0 && (result/val) != mult {
		return -1, fmt.Errorf("Overflow multiplying %d with %d", val, mult)
	}

	return result, nil
}

// byteSuffixMultiplicators maps byte size suffixes to their multiplicator.
var byteSuffixMultiplicators = map[string]int64{
	"":       1,
	"B":      1,
	" bytes": 1,
	"kB":     1000,
	"MB":     1000 * 1000,
	"GB":     1000 * 1000 * 1000,
	"TB":     1000 * 1000 * 1000 * 1000,
	"PB":     1000 * 1000 * 1000 * 1000 * 1000,
	"EB":     1000 * 1000 * 1000 * 1000 * 1000 * 1000,
	"KiB":    1024,
	"MiB":    1024 * 1024,
	"GiB":    1024 * 1024 * 1024,
	"TiB":    1024 * 1024 * 1024 * 1024,
	"PiB":    1024 * 1024 * 1024 * 1024 * 1024,
	"EiB":    1024 * 1024 * 1024 * 1024 * 1024 * 1024,
}

// bitSuffixMultiplicators maps bit size suffixes to their multiplicator.
var bitSuffixMultiplicators = map[string]int64{
	"":      1,
	"bit":   1,
	"kbit":  1000,
	"Mbit":  1000 * 1000,
	"Gbit":  1000 * 1000 * 1000,
	"Tbit":  1000 * 1000 * 1000 * 1000,
	"Pbit":  1000 * 1000 * 1000 * 1000 * 1000,
	"Ebit":  1000 * 1000 * 1000 * 1000 * 1000 * 1000,
	"Kibit": 1024,
	"Mibit": 1024 * 1024,
	"Gibit": 1024 * 1024 * 1024,
	"Tibit": 1024 * 1024 * 1024 * 1024,
	"Pibit": 1024 * 1024 * 1024 * 1024 * 1024,
	"Eibit": 1024 * 1024 * 1024 * 1024 * 1024 * 1024,
}

// parseSizeString parses a human representation of an amount of data into a
// number of base units using the provided suffix multiplicators. The
// unknownSuffixErr callback produces the error returned for an unrecognized
// suffix.
func parseSizeString(input string, multiplicators map[string]int64, unknownSuffixErr func(input string, suffix string) error) (int64, error) {
	// Empty input
	if input == "" {
		return 0, nil
	}

	// Find where the suffix begins
	suffixLen := 0
	for i, chr := range []byte(input) {
		_, err := strconv.Atoi(string([]byte{chr}))
		if err != nil {
			suffixLen = len(input) - i
			break
		}
	}

	if suffixLen == len(input) {
		return -1, fmt.Errorf("Invalid value: %s", input)
	}

	// Extract the suffix
	suffix := input[len(input)-suffixLen:]

	// Extract the value
	value := input[0 : len(input)-suffixLen]
	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("Invalid integer: %s", input)
	}

	// Figure out the multiplicator
	multiplicator, ok := multiplicators[suffix]
	if !ok {
		return -1, unknownSuffixErr(input, suffix)
	}

	return handleOverflow(valueInt, multiplicator)
}

// ParseByteSizeString parses a human representation of an amount of
// data into a number of bytes.
func ParseByteSizeString(input string) (int64, error) {
	return parseSizeString(input, byteSuffixMultiplicators, func(input string, _ string) error {
		return fmt.Errorf("Invalid value: %s", input)
	})
}

// ParseBitSizeString parses a human representation of an amount of
// data into a number of bits.
func ParseBitSizeString(input string) (int64, error) {
	return parseSizeString(input, bitSuffixMultiplicators, func(_ string, suffix string) error {
		return fmt.Errorf("Unsupported suffix: %s", suffix)
	})
}

type integer interface {
	~int64 | ~uint64
}

// getSizeString takes a number of base units, precision, a divisor and the
// ordered unit suffixes, and returns a human representation of the amount of
// data.
func getSizeString[T integer](input T, precision uint, divisor float64, units []string) string {
	value := float64(input)
	if value < divisor {
		return fmt.Sprintf("%dB", input)
	}

	for _, unit := range units {
		value = value / divisor
		if value < divisor {
			return fmt.Sprintf("%.*f%s", precision, value, unit)
		}
	}

	return fmt.Sprintf("%.*fEB", precision, value)
}

// GetByteSizeString takes a number of bytes and precision and returns a
// human representation of the amount of data.
func GetByteSizeString[T integer](input T, precision uint) string {
	return getSizeString(input, precision, 1000, []string{"kB", "MB", "GB", "TB", "PB", "EB"})
}

// GetByteSizeStringIEC takes a number of bytes and precision and returns a
// human representation of the amount of data using IEC units.
func GetByteSizeStringIEC[T integer](input T, precision uint) string {
	return getSizeString(input, precision, 1024, []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"})
}
