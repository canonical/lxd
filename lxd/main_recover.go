package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/validate"
)

type cmdRecover struct {
	global *cmdGlobal
}

func (c *cmdRecover) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "recover"
	cmd.Short = "Recover missing instances and volumes from existing and unknown storage pools"
	cmd.Long = `Description:
	Recover missing instances and volumes from existing and unknown storage pools

  This command is mostly used for disaster recovery. It will ask you about unknown storage pools and attempt to
  access them, along with existing storage pools, and identify any missing instances and volumes that exist on the
  pools but are not in the LXD database. It will then offer to recreate these database records.
`
	cmd.RunE = c.run

	return cmd
}

func (c *cmdRecover) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if len(args) > 0 {
		return errors.New("Invalid arguments")
	}

	d, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	var reqValidate internalRecoverValidatePost

	for {
		// Get list of existing storage pools to scan.
		existingPools, err := d.GetStoragePools()
		if err != nil {
			return fmt.Errorf("Failed getting existing storage pools: %w", err)
		}

		if len(existingPools) == 0 {
			return errors.New(`There are no storage pools defined in the database. To recover pools that exist in the storage device already, run "lxc storage create" for each pool with its source configuration and "source.recover=true" setting.`)
		}

		fmt.Println("This LXD server currently has the following storage pools:")
		for _, existingPool := range existingPools {
			fmt.Printf(" - Pool %q using driver %q\n", existingPool.Name, existingPool.Driver)
		}

		proceed, err := c.global.asker.AskBool("Would you like to continue with scanning for lost volumes? (yes/no) [default=yes]: ", "yes")
		if err != nil {
			return err
		}

		if !proceed {
			return nil
		}

		fmt.Println("Scanning for unknown volumes...")

		// Send /internal/recover/validate request to LXD.
		reqValidate = internalRecoverValidatePost{
			Pools: make([]api.StoragePoolsPost, 0, len(existingPools)),
		}

		// Add existing pools to request.
		for _, p := range existingPools {
			reqValidate.Pools = append(reqValidate.Pools, api.StoragePoolsPost{
				Name: p.Name, // Only send existing pool name, the rest will be looked up on server.
			})
		}

		resp, _, err := d.RawQuery(http.MethodPost, "/internal/recover/validate", reqValidate, "")
		if err != nil {
			return fmt.Errorf("Failed validation request: %w", err)
		}

		var res internalRecoverValidateResult

		err = resp.MetadataAsStruct(&res)
		if err != nil {
			return fmt.Errorf("Failed parsing validation response: %w", err)
		}

		if len(res.UnknownVolumes) > 0 {
			fmt.Println("The following unknown volumes have been found:")
			for _, unknownVol := range res.UnknownVolumes {
				fmt.Printf(" - %s %q on pool %q in project %q (includes %d snapshots)\n", cases.Title(language.English).String(unknownVol.Type), unknownVol.Name, unknownVol.Pool, unknownVol.Project, unknownVol.SnapshotCount)
			}
		}

		if len(res.DependencyErrors) > 0 {
			fmt.Println("You are currently missing the following:")
			for _, depErr := range res.DependencyErrors {
				fmt.Printf(" - %s\n", depErr)
			}

			_, _ = c.global.asker.AskString("Please create those missing entries and then hit ENTER: ", "", validate.Optional())
			continue
		}

		if len(res.UnknownVolumes) == 0 {
			fmt.Println("No unknown storage volumes found. Nothing to do.")
			return nil
		}

		break // Dependencies met.
	}

	proceed, err := c.global.asker.AskBool("Would you like those to be recovered? (yes/no) [default=no]: ", "no")
	if err != nil {
		return err
	}

	if !proceed {
		return nil
	}

	fmt.Println("Starting recovery...")

	// Send /internal/recover/import request to LXD.
	// Don't lint next line with staticcheck. It says we should convert reqValidate directly to an internalRecoverImportPost
	// because their types are identical. This is less clear and will not work if either type changes in the future.
	reqImport := internalRecoverImportPost{ //nolint:staticcheck
		Pools: reqValidate.Pools,
	}

	_, _, err = d.RawQuery(http.MethodPost, "/internal/recover/import", reqImport, "")
	if err != nil {
		return fmt.Errorf("Failed import request: %w", err)
	}

	return nil
}
