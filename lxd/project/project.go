package project

import (
	"fmt"
)

// Prefix Add the "<project>_" prefix when the given project name is not "default".
func Prefix(project string, s string) string {
	if project != "default" {
		s = fmt.Sprintf("%s_%s", project, s)
	}
	return s
}
