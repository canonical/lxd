package connection

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
)

// ParseURI parses the given sqlite3 URI checking if it's compatible with
// dqlite.
//
// Only pure file names without any directory segment are accepted
// (e.g. "test.db"). Query parameters are always valid except for
// "mode=memory".
//
// It returns the filename and query parameters.
func ParseURI(uri string) (string, uint64, error) {
	filename := uri
	flags := uint64(bindings.OpenReadWrite | bindings.OpenCreate)

	pos := strings.IndexRune(uri, '?')
	if pos >= 1 {
		params, err := url.ParseQuery(uri[pos+1:])
		if err != nil {
			return "", 0, err
		}

		mode := params.Get("mode")
		switch mode {
		case "":
		case "memory":
			return "", 0, fmt.Errorf("memory database not supported")
		case "ro":
			flags = bindings.OpenReadOnly
		case "rw":
			flags = bindings.OpenReadWrite
		case "rwc":
			flags = bindings.OpenReadWrite | bindings.OpenCreate
		default:
			return "", 0, fmt.Errorf("unknown mode %s", mode)
		}

		filename = filename[:pos]
	}

	if strings.HasPrefix(filename, "file:") {
		filename = filename[len("file:"):]
	}

	if filename == ":memory:" {
		return "", 0, fmt.Errorf("memory database not supported")
	}

	if strings.IndexRune(filename, '/') >= 0 {
		return "", 0, fmt.Errorf("directory segments are invalid")
	}

	return filename, flags, nil
}
