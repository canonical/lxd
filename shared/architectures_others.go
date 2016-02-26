// +build !linux

package shared

func ArchitectureGetLocal() (string, error) {
	return ArchitectureDefault, nil
}
