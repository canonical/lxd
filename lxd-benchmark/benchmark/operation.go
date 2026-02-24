package benchmark

import (
	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

func createContainer(c lxd.InstanceServer, fingerprint string, name string, privileged bool) error {
	config := map[string]string{}
	if privileged {
		config["security.privileged"] = "true"
	}

	config[userConfigKey] = "true"

	req := api.InstancesPost{
		Name: name,
		Source: api.InstanceSource{
			Type:        api.SourceTypeImage,
			Fingerprint: fingerprint,
		},
	}

	req.Config = config

	op, err := c.CreateInstance(req)
	if err != nil {
		return err
	}

	return op.Wait()
}

func startContainer(c lxd.InstanceServer, name string) error {
	op, err := c.UpdateInstanceState(
		name, api.InstanceStatePut{Action: "start", Timeout: -1}, "")
	if err != nil {
		return err
	}

	return op.Wait()
}

func stopContainer(c lxd.InstanceServer, name string) error {
	op, err := c.UpdateInstanceState(
		name, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true}, "")
	if err != nil {
		return err
	}

	return op.Wait()
}

func freezeContainer(c lxd.InstanceServer, name string) error {
	op, err := c.UpdateInstanceState(
		name, api.InstanceStatePut{Action: "freeze", Timeout: -1}, "")
	if err != nil {
		return err
	}

	return op.Wait()
}

func deleteContainer(c lxd.InstanceServer, name string) error {
	op, err := c.DeleteInstance(name, false)
	if err != nil {
		return err
	}

	return op.Wait()
}

func copyImage(c lxd.InstanceServer, s lxd.ImageServer, image api.Image) error {
	op, err := c.CopyImage(s, image, nil)
	if err != nil {
		return err
	}

	return op.Wait()
}
