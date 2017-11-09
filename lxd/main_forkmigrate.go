package main

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/lxc/go-lxc.v2"
)

/*
 * Similar to forkstart, this is called when lxd is invoked as:
 *
 *    lxd forkmigrate <container> <lxcpath> <path_to_config> <path_to_criu_images> <preserves_inodes>
 *
 * liblxc's restore() sets up the processes in such a way that the monitor ends
 * up being a child of the process that calls it, in our case lxd. However, we
 * really want the monitor to be daemonized, so we fork again. Additionally, we
 * want to fork for the same reasons we do forkstart (i.e. reduced memory
 * footprint when we fork tasks that will never free golang's memory, etc.)
 */
func cmdForkMigrate(args *Args) error {
	if len(args.Params) != 5 {
		return fmt.Errorf("Bad arguments %q", args.Params)
	}

	name := args.Params[0]
	lxcpath := args.Params[1]
	configPath := args.Params[2]
	imagesDir := args.Params[3]

	preservesInodes, err := strconv.ParseBool(args.Params[4])
	if err != nil {
		return err
	}

	c, err := lxc.NewContainer(name, lxcpath)
	if err != nil {
		return err
	}

	if err := c.LoadConfigFile(configPath); err != nil {
		return err
	}

	/* see https://github.com/golang/go/issues/13155, startContainer, and dc3a229 */
	os.Stdin.Close()
	os.Stdout.Close()
	os.Stderr.Close()

	return c.Migrate(lxc.MIGRATE_RESTORE, lxc.MigrateOptions{
		Directory:       imagesDir,
		Verbose:         true,
		PreservesInodes: preservesInodes,
	})
}
