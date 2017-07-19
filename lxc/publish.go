package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"

	"github.com/lxc/lxd/shared"
)

type publishCmd struct {
	pAliases             aliasList // aliasList defined in lxc/image.go
	compressionAlgorithm string
	makePublic           bool
	Force                bool
}

func (c *publishCmd) showByDefault() bool {
	return true
}

func (c *publishCmd) usage() string {
	return i18n.G(
		`Usage: lxc publish [<remote>:]<container>[/<snapshot>] [<remote>:] [--alias=ALIAS...] [prop-key=prop-value...]

Publish containers as images.`)
}

func (c *publishCmd) flags() {
	gnuflag.BoolVar(&c.makePublic, "public", false, i18n.G("Make the image public"))
	gnuflag.Var(&c.pAliases, "alias", i18n.G("New alias to define at target"))
	gnuflag.BoolVar(&c.Force, "force", false, i18n.G("Stop the container if currently running"))
	gnuflag.BoolVar(&c.Force, "f", false, i18n.G("Stop the container if currently running"))
	gnuflag.StringVar(&c.compressionAlgorithm, "compression", "", i18n.G("Define a compression algorithm: for image or none"))
}

func (c *publishCmd) run(conf *config.Config, args []string) error {
	iName := ""
	iRemote := ""
	properties := map[string]string{}
	firstprop := 1 // first property is arg[2] if arg[1] is image remote, else arg[1]

	if len(args) < 1 {
		return errArgs
	}

	cRemote, cName, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	if len(args) >= 2 && !strings.Contains(args[1], "=") {
		firstprop = 2
		iRemote, iName, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}
	} else {
		iRemote, iName, err = conf.ParseRemote("")
		if err != nil {
			return err
		}
	}

	if cName == "" {
		return fmt.Errorf(i18n.G("Container name is mandatory"))
	}
	if iName != "" {
		return fmt.Errorf(i18n.G("There is no \"image name\".  Did you want an alias?"))
	}

	d, err := conf.GetContainerServer(iRemote)
	if err != nil {
		return err
	}

	s := d
	if cRemote != iRemote {
		s, err = conf.GetContainerServer(cRemote)
		if err != nil {
			return err
		}
	}

	if !shared.IsSnapshot(cName) {
		ct, etag, err := s.GetContainer(cName)
		if err != nil {
			return err
		}

		wasRunning := ct.StatusCode != 0 && ct.StatusCode != api.Stopped
		wasEphemeral := ct.Ephemeral

		if wasRunning {
			if !c.Force {
				return fmt.Errorf(i18n.G("The container is currently running. Use --force to have it stopped and restarted."))
			}

			if ct.Ephemeral {
				ct.Ephemeral = false
				op, err := s.UpdateContainer(cName, ct.Writable(), etag)
				if err != nil {
					return err
				}

				err = op.Wait()
				if err != nil {
					return err
				}

				// Refresh the ETag
				_, etag, err = s.GetContainer(cName)
				if err != nil {
					return err
				}
			}

			req := api.ContainerStatePut{
				Action:  string(shared.Stop),
				Timeout: -1,
				Force:   true,
			}

			op, err := s.UpdateContainerState(cName, req, "")
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return fmt.Errorf(i18n.G("Stopping container failed!"))
			}

			defer func() {
				req.Action = string(shared.Start)
				op, err = s.UpdateContainerState(cName, req, "")
				if err != nil {
					return
				}

				op.Wait()
			}()

			if wasEphemeral {
				ct.Ephemeral = true
				op, err := s.UpdateContainer(cName, ct.Writable(), etag)
				if err != nil {
					return err
				}

				err = op.Wait()
				if err != nil {
					return err
				}
			}
		}
	}

	for i := firstprop; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return errArgs
		}
		properties[entry[0]] = entry[1]
	}

	// We should only set the properties field if there actually are any.
	// Otherwise we will only delete any existing properties on publish.
	// This is something which only direct callers of the API are allowed to
	// do.
	if len(properties) == 0 {
		properties = nil
	}

	// Reformat aliases
	aliases := []api.ImageAlias{}
	for _, entry := range c.pAliases {
		alias := api.ImageAlias{}
		alias.Name = entry
		aliases = append(aliases, alias)
	}

	// Create the image
	req := api.ImagesPost{
		Source: &api.ImagesPostSource{
			Type: "container",
			Name: cName,
		},
		CompressionAlgorithm: c.compressionAlgorithm,
	}
	req.Properties = properties

	if shared.IsSnapshot(cName) {
		req.Source.Type = "snapshot"
	}

	if cRemote == iRemote {
		req.Public = c.makePublic
	}

	op, err := s.CreateImage(req, nil)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	// Grab the fingerprint
	fingerprint := op.Metadata["fingerprint"].(string)

	// For remote publish, copy to target now
	if cRemote != iRemote {
		defer s.DeleteImage(fingerprint)

		// Get the source image
		image, _, err := s.GetImage(fingerprint)
		if err != nil {
			return err
		}

		// Image copy arguments
		args := lxd.ImageCopyArgs{
			Public: c.makePublic,
		}

		// Copy the image to the destination host
		op, err := d.CopyImage(s, *image, &args)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	err = ensureImageAliases(d, aliases, fingerprint)
	if err != nil {
		return err
	}
	fmt.Printf(i18n.G("Container published with fingerprint: %s")+"\n", fingerprint)

	return nil
}
