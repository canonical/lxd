package util

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// unprivilegedCopyWarnOnce makes sure the warning about the missing unprivileged user is only logged once.
var unprivilegedCopyWarnOnce sync.Once

// UnprivilegedCopy copies src into dst as uid and gid, so that a filesystem quota configured for that user
// applies to the write.
//
// Ownership of dst is handed over to uid and gid first, as filesystem quotas are accounted against the
// owner of the file and not against the writing process. The copy itself runs through RunUnprivileged, as
// a writer holding CAP_SYS_RESOURCE bypasses both the soft and the hard quota limit.
//
// When uid or gid is 0 there is nothing to switch to and a regular copy is performed instead.
//
// If the write exceeds the configured quota, an error matching http.StatusRequestEntityTooLarge is
// returned.
func UnprivilegedCopy(ctx context.Context, dst *os.File, src io.Reader, uid uint32, gid uint32) (int64, error) {
	if uid == 0 || gid == 0 {
		unprivilegedCopyWarnOnce.Do(func() {
			logger.Warn("No unprivileged user or group available, uploads are not covered by filesystem quotas")
		})

		return io.Copy(dst, src)
	}

	err := dst.Chown(int(uid), int(gid))
	if err != nil {
		return 0, fmt.Errorf("Failed changing ownership of temporary file: %w", err)
	}

	var written int64
	err = RunUnprivileged(ctx, uid, gid, func() error {
		var err error
		written, err = io.Copy(dst, src)
		return err
	})
	if errors.Is(err, unix.EDQUOT) {
		return written, api.StatusErrorf(http.StatusRequestEntityTooLarge, "Upload exceeds the storage quota")
	}

	return written, err
}
