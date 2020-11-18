package main

import (
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"github.com/grant-he/lxd/client"
)

type cmdImport struct {
	global *cmdGlobal

	flagForce   bool
	flagProject string
}

func (c *cmdImport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "import <container name>"
	cmd.Short = "Import existing containers"
	cmd.Long = `Description:
  Import existing containers

  This command is mostly used for disaster recovery. It lets you attempt
  to recreate all database entries for containers that LXD no longer knows
  about.

  To do so, you must first mount your container storage at the expected
  path inside the storage-pools directory. Once that's in place,
  ` + "`lxd import`" + ` can be called for each individual container.
`
	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, "Force the import (override existing data or partial restore)")
	cmd.Flags().StringVar(&c.flagProject, "project", "", "Specify the project")

	return cmd
}

func (c *cmdImport) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	if len(args) < 1 {
		cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	name := args[0]
	req := map[string]interface{}{
		"name":  name,
		"force": c.flagForce,
	}

	d, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	v := url.Values{}
	v.Set("project", c.flagProject)

	_, _, err = d.RawQuery("POST", fmt.Sprintf("/internal/containers?%s", v.Encode()), req, "")
	if err != nil {
		return err
	}

	return nil
}
