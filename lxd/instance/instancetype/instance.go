package instancetype

import (
	"fmt"
	"strings"
)

// ValidSnapName validates a snapshot instance name which must not include the instance prefix.
func ValidSnapName(snapshotName string) error {
	if snapshotName == ".." {
		return fmt.Errorf("Invalid instance snapshot name %q", snapshotName)
	}

	if strings.ContainsAny(snapshotName, "* /\\") {
		return fmt.Errorf("Invalid instance snapshot name %q: Cannot contain *, spaces, forward or back slashes", snapshotName)
	}

	return nil
}
