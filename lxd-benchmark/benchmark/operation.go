package benchmark

import (
	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
)

func createContainer(c lxd.ContainerServer, fingerprint string, name string, privileged bool) error {
	config := map[string]string{}
	if privileged {
		config["security.privileged"] = "true"
	}
	config["user.lxd-benchmark"] = "true"

	req := api.ContainersPost{
		Name: name,
		Source: api.ContainerSource{
			Type:        "image",
			Fingerprint: fingerprint,
		},
	}
	req.Config = config

	op, err := c.CreateContainer(req)
	if err != nil {
		return err
	}

	return op.Wait()
}

func stopContainer(c lxd.ContainerServer, name string) error {
	op, err := c.UpdateContainerState(
		name, api.ContainerStatePut{Action: "stop", Timeout: -1, Force: true}, "")
	if err != nil {
		return err
	}

	return op.Wait()
}
