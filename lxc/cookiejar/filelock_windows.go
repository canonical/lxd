//go:build windows

package cookiejar

import (
	"net/http"
	"os"

	"github.com/canonical/lxd/shared/api"
)

// The windows client does not implement file locking.

func unlockFile(f *os.File) error {
	return api.NewStatusError(http.StatusNotImplemented, "Locking is not implemented on windows")
}

func lockFile(f *os.File) error {
	return api.NewStatusError(http.StatusNotImplemented, "Locking is not implemented on windows")
}

func rLockFile(f *os.File) error {
	return api.NewStatusError(http.StatusNotImplemented, "Locking is not implemented on windows")
}
