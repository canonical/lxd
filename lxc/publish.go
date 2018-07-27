package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"

	"github.com/lxc/lxd/shared"
)

type cmdPublish struct {
	global *cmdGlobal

	flagAliases              []string
	flagCompressionAlgorithm string
	flagMakePublic           bool
	flagForce                bool
}

func (c *cmdPublish) showByDefault() bool {
	return true
}

func (c *cmdPublish) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("publish [<remote>:]<container>[/<snapshot>] [<remote>:] [flags] [key=value...]")
	cmd.Short = i18n.G("Publish containers as images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Publish containers as images`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagMakePublic, "public", false, i18n.G("Make the image public"))
	cmd.Flags().StringArrayVar(&c.flagAliases, "alias", nil, i18n.G("New alias to define at target")+"``")
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Stop the container if currently running"))
	cmd.Flags().StringVar(&c.flagCompressionAlgorithm, "compression", "", i18n.G("Define a compression algorithm: for image or none")+"``")

	return cmd
}

func (c *cmdPublish) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	iName := ""
	iRemote := ""
	properties := map[string]string{}
	firstprop := 1 // first property is arg[2] if arg[1] is image remote, else arg[1]

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
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
			if !c.flagForce {
				return fmt.Errorf(i18n.G("The container is currently running. Use --force to have it stopped and restarted"))
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
			return fmt.Errorf(i18n.G("Bad key=value pair: %s"), entry)
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
	for _, entry := range c.flagAliases {
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
		CompressionAlgorithm: c.flagCompressionAlgorithm,
	}
	req.Properties = properties

	if shared.IsSnapshot(cName) {
		req.Source.Type = "snapshot"
	}

	if cRemote == iRemote {
		req.Public = c.flagMakePublic
	}

	op, err := s.CreateImage(req, nil)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}
	opAPI := op.Get()

	// Grab the fingerprint
	fingerprint := opAPI.Metadata["fingerprint"].(string)

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
			Public: c.flagMakePublic,
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
