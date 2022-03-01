package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/validate"
)

type cmdRecover struct {
	global *cmdGlobal
}

func (c *cmdRecover) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "recover"
	cmd.Short = "Recover missing instances and volumes from existing and unknown storage pools"
	cmd.Long = `Description:
	Recover missing instances and volumes from existing and unknown storage pools

  This command is mostly used for disaster recovery. It will ask you about unknown storage pools and attempt to
  access them, along with existing storage pools, and identify any missing instances and volumes that exist on the
  pools but are not in the LXD database. It will then offer to recreate these database records.
`
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdRecover) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if len(args) > 0 {
		return fmt.Errorf("Invalid arguments")
	}

	d, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	server, _, err := d.GetServer()
	if err != nil {
		return err
	}

	isClustered := d.IsClustered()

	// Get list of existing storage pools to scan.
	existingPools, err := d.GetStoragePools()
	if err != nil {
		return fmt.Errorf("Failed getting existing storage pools: %w", err)
	}

	fmt.Print("This LXD server currently has the following storage pools:\n")
	for _, existingPool := range existingPools {
		fmt.Printf(" - %s (backend=%q, source=%q)\n", existingPool.Name, existingPool.Driver, existingPool.Config["source"])
	}

	unknownPools := make([]api.StoragePoolsPost, 0, len(existingPools))

	// Build up a list of unknown pools to scan.
	// We don't offer this option if the server is clustered because we don't allow creating storage pools on
	// an individual server when clustered.
	if !isClustered {
		var supportedDriverNames []string

		for {
			addUnknownPool, err := cli.AskBool("Would you like to recover another storage pool? (yes/no) [default=no]: ", "no")
			if err != nil {
				return err
			}

			if !addUnknownPool {
				break
			}

			// Get available storage drivers if not done already.
			if supportedDriverNames == nil {
				for _, supportedDriver := range server.Environment.StorageSupportedDrivers {
					supportedDriverNames = append(supportedDriverNames, supportedDriver.Name)
				}
			}

			unknownPool := api.StoragePoolsPost{
				StoragePoolPut: api.StoragePoolPut{
					Config: make(map[string]string),
				},
			}

			unknownPool.Name, err = cli.AskString("Name of the storage pool: ", "", validate.Required(func(value string) error {
				if value == "" {
					return fmt.Errorf("Pool name cannot be empty")
				}

				for _, p := range unknownPools {
					if value == p.Name {
						return fmt.Errorf("Storage pool %q is already on recover list", value)
					}
				}

				return nil
			}))
			if err != nil {
				return err
			}

			unknownPool.Driver, err = cli.AskString(fmt.Sprintf("Name of the storage backend (%s): ", strings.Join(supportedDriverNames, ", ")), "", validate.IsOneOf(supportedDriverNames...))
			if err != nil {
				return err
			}

			unknownPool.Config["source"], err = cli.AskString("Source of the storage pool (block device, volume group, dataset, path, ... as applicable): ", "", validate.IsNotEmpty)
			if err != nil {
				return err
			}

			for {
				var configKey, configValue string
				cli.AskString("Additional storage pool configuration property (KEY=VALUE, empty when done): ", "", validate.Optional(func(value string) error {
					configParts := strings.SplitN(value, "=", 2)
					if len(configParts) < 2 {
						return fmt.Errorf("Config option should be in the format KEY=VALUE")
					}

					configKey = configParts[0]
					configValue = configParts[1]

					return nil
				}))

				if configKey == "" {
					break
				}

				unknownPool.Config[configKey] = configValue
			}

			unknownPools = append(unknownPools, unknownPool)
		}
	}

	fmt.Printf("The recovery process will be scanning the following storage pools:\n")
	for _, p := range existingPools {
		fmt.Printf(" - EXISTING: %q (backend=%q, source=%q)\n", p.Name, p.Driver, p.Config["source"])
	}

	for _, p := range unknownPools {
		fmt.Printf(" - NEW: %q (backend=%q, source=%q)\n", p.Name, p.Driver, p.Config["source"])
	}

	proceed, err := cli.AskBool("Would you like to continue with scanning for lost volumes? (yes/no) [default=yes]: ", "yes")
	if err != nil {
		return err
	}

	if !proceed {
		return nil
	}

	fmt.Print("Scanning for unknown volumes...\n")

	// Send /internal/recover/validate request to LXD.
	reqValidate := internalRecoverValidatePost{
		Pools: make([]api.StoragePoolsPost, 0, len(existingPools)+len(unknownPools)),
	}

	// Add existing pools to request.
	for _, p := range existingPools {
		reqValidate.Pools = append(reqValidate.Pools, api.StoragePoolsPost{
			Name: p.Name, // Only send existing pool name, the rest will be looked up on server.
		})
	}

	// Add unknown pools to request.
	reqValidate.Pools = append(reqValidate.Pools, unknownPools...)

	for {
		resp, _, err := d.RawQuery("POST", "/internal/recover/validate", reqValidate, "")
		if err != nil {
			return fmt.Errorf("Failed validation request: %w", err)
		}

		var res internalRecoverValidateResult

		err = resp.MetadataAsStruct(&res)
		if err != nil {
			return fmt.Errorf("Failed parsing validation response: %w", err)
		}

		if len(res.UnknownVolumes) > 0 {
			fmt.Print("The following unknown volumes have been found:\n")
			for _, unknownVol := range res.UnknownVolumes {
				fmt.Printf(" - %s %q on pool %q in project %q (includes %d snapshots)\n", strings.Title(unknownVol.Type), unknownVol.Name, unknownVol.Pool, unknownVol.Project, unknownVol.SnapshotCount)
			}
		}

		if len(res.DependencyErrors) > 0 {
			fmt.Print("You are currently missing the following:\n")

			for _, depErr := range res.DependencyErrors {
				fmt.Printf(" - %s\n", depErr)
			}

			cli.AskString("Please create those missing entries and then hit ENTER: ", "", validate.Optional())
		} else {
			if len(res.UnknownVolumes) <= 0 {
				fmt.Print("No unknown volumes found. Nothing to do.\n")
				return nil
			}

			break // Dependencies met.
		}
	}

	proceed, err = cli.AskBool("Would you like those to be recovered? (yes/no) [default=no]: ", "no")
	if err != nil {
		return err
	}

	if !proceed {
		return nil
	}

	fmt.Print("Starting recovery...\n")

	// Send /internal/recover/import request to LXD.
	reqImport := internalRecoverImportPost{
		Pools: reqValidate.Pools,
	}

	_, _, err = d.RawQuery("POST", "/internal/recover/import", reqImport, "")
	if err != nil {
		return fmt.Errorf("Failed import request: %w", err)
	}

	return nil
}
