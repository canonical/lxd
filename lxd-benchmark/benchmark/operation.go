package benchmark

import (
	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
)

func stopContainer(c lxd.ContainerServer, name string) error {
	op, err := c.UpdateContainerState(
		name, api.ContainerStatePut{Action: "stop", Timeout: -1, Force: true}, "")
	if err != nil {
		return err
	}

	return op.Wait()
}
