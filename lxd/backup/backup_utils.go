package backup

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
)

// TarReader rewinds backup file handle r and returns new tar reader and process cleanup function.
func TarReader(r io.ReadSeeker) (*tar.Reader, context.CancelFunc, error) {
	r.Seek(0, 0)
	_, _, unpacker, err := shared.DetectCompressionFile(r)
	if err != nil {
		return nil, nil, err
	}

	if unpacker == nil {
		return nil, nil, fmt.Errorf("Unsupported backup compression")
	}

	tr, cancelFunc, err := shared.CompressedTarReader(context.Background(), r, unpacker)
	if err != nil {
		return nil, nil, err
	}

	return tr, cancelFunc, nil
}

// Lifecycle emits a backup-specific lifecycle event.
func Lifecycle(s *state.State, inst Instance, name string, action string, ctx map[string]interface{}) error {
	prefix := "instance-backup"
	u := fmt.Sprintf("/1.0/instances/%s/backups/%s", url.PathEscape(inst.Name()), url.PathEscape(name))

	if inst.Project() != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(inst.Project()))
	}

	return s.Events.SendLifecycle(inst.Project(), fmt.Sprintf("%s-%s", prefix, action), u, ctx)
}
