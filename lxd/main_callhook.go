package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxd-user/callhook"
	"github.com/canonical/lxd/lxd/device/cdi"
)

type cmdCallhook struct {
	global            *cmdGlobal
	devicesRootFolder string
}

// Command returns a cobra command for `lxd callhook`.
func (c *cmdCallhook) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "callhook <path> [<instance id>|<instance project> <instance name>] <hook>"
	cmd.Short = "Call container lifecycle hook in LXD"
	cmd.Long = `Description:
  Call container lifecycle hook in LXD

  This internal command notifies LXD about a container lifecycle event
  (start, startmountns, stopns, stop, restart) and blocks until LXD has processed it.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	// devicesRootFolder is used to specify where to look for CDI config device files.
	cmd.Flags().StringVar(&c.devicesRootFolder, "devicesRootFolder", "", "Root folder for CDI devices")

	return cmd
}

// resolveTargetRelativeToLink converts a target relative to a link path into an absolute path.
func resolveTargetRelativeToLink(link string, target string) (string, error) {
	if !filepath.IsAbs(link) {
		return "", fmt.Errorf("The link must be an absolute path: %q (target: %q)", link, target)
	}

	if filepath.IsAbs(target) {
		return target, nil
	}

	linkDir := filepath.Dir(link)
	absTarget := filepath.Join(linkDir, target)
	cleanPath := filepath.Clean(absTarget)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", err
	}

	return absPath, nil
}

// customCDILinkerConfFile is the name of the linker conf file we will write to
// inside the container. The `00-lxdcdi` prefix is chosen to ensure that these libraries have
// a higher precedence than other libraries on the system.
var customCDILinkerConfFile = "00-lxdcdi.conf"

// applyCDIHooksToContainer is called before the container has started but after the container namespace has been setup,
// and is used whenever CDI devices are added to a container and where symlinks and linker cache entries need to be created.
// These entries are listed in a 'CDI hooks file' located at `hooksFilePath`.
func applyCDIHooksToContainer(devicesRootFolder string, hooksFilePath string) error {
	hookFile, err := os.Open(filepath.Join(devicesRootFolder, hooksFilePath))
	if err != nil {
		return fmt.Errorf("Failed to open the CDI hooks file at %q: %w", hooksFilePath, err)
	}

	defer hookFile.Close()

	hooks := &cdi.Hooks{}
	err = json.NewDecoder(hookFile).Decode(hooks)
	if err != nil {
		return fmt.Errorf("Failed to decode the CDI hooks file at %q: %w\n", hooksFilePath, err)
	}

	fmt.Println("CDI Hooks file loaded:")
	prettyHooks, err := json.MarshalIndent(hooks, "", "  ")
	if err != nil {
		return err
	}

	containerRootFSMount := os.Getenv("LXC_ROOTFS_MOUNT")
	if containerRootFSMount == "" {
		return fmt.Errorf("LXC_ROOTFS_MOUNT is empty")
	}

	fmt.Println(string(prettyHooks))

	// Creating the symlinks
	for _, symlink := range hooks.Symlinks {
		// Resolve hook link from target
		absTarget, err := resolveTargetRelativeToLink(symlink.Link, symlink.Target)
		if err != nil {
			return fmt.Errorf("Failed to resolve a CDI symlink: %w\n", err)
		}

		// Try to create the directory if it doesn't exist
		err = os.MkdirAll(filepath.Dir(filepath.Join(containerRootFSMount, symlink.Link)), 0755)
		if err != nil {
			return fmt.Errorf("Failed to create the directory for the CDI symlink: %w\n", err)
		}

		// Create the symlink
		err = os.Symlink(absTarget, filepath.Join(containerRootFSMount, symlink.Link))
		if err != nil {
			if !os.IsExist(err) {
				return fmt.Errorf("Failed to create the CDI symlink: %w\n", err)
			}

			fmt.Printf("Symlink not created because link %q already exists for target %q\n", symlink.Link, absTarget)
		}
	}

	// Updating the linker cache
	l := len(hooks.LDCacheUpdates)
	if l > 0 {
		ldConfFilePath := fmt.Sprintf("%s/etc/ld.so.conf.d/%s", containerRootFSMount, customCDILinkerConfFile)
		_, err = os.Stat(ldConfFilePath)
		if err == nil {
			// The file already exists. Read it first, analyze its entries
			// and add the ones that are not already there.
			ldConfFile, err := os.OpenFile(ldConfFilePath, os.O_APPEND|os.O_RDWR, 0644)
			if err != nil {
				return fmt.Errorf("Failed to open the ld.so.conf file at %q: %w\n", ldConfFilePath, err)
			}

			existingLinkerEntries := make(map[string]bool)
			scanner := bufio.NewScanner(ldConfFile)
			for scanner.Scan() {
				existingLinkerEntries[strings.TrimSpace(scanner.Text())] = true
			}

			fmt.Printf("Existing linker entries: %v\n", existingLinkerEntries)
			for _, update := range hooks.LDCacheUpdates {
				if !existingLinkerEntries[update] {
					fmt.Printf("Adding linker entry: %s\n", update)
					_, err = fmt.Fprintln(ldConfFile, update)
					if err != nil {
						ldConfFile.Close()
						return fmt.Errorf("Failed to write to the linker conf file at %q: %w\n", ldConfFilePath, err)
					}

					existingLinkerEntries[update] = true
				}
			}

			ldConfFile.Close()
		} else if errors.Is(err, os.ErrNotExist) {
			// The file does not exist. We simply create it with our entries.
			ldConfFile, err := os.OpenFile(ldConfFilePath, os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return fmt.Errorf("Failed to create the linker conf file at %q: %w\n", ldConfFilePath, err)
			}

			for _, update := range hooks.LDCacheUpdates {
				fmt.Printf("Adding linker entry: %s\n", update)
				_, err = fmt.Fprintln(ldConfFile, update)
				if err != nil {
					ldConfFile.Close()
					return fmt.Errorf("Failed to write to the linker conf file at %q: %w\n", ldConfFilePath, err)
				}
			}

			ldConfFile.Close()
		} else {
			return fmt.Errorf("Could not stat the linker conf file to add CDI linker entries at %q: %w\n", ldConfFilePath, err)
		}
	}

	// Then remove the linker cache and regenerate it
	linkerCachePath := filepath.Join(containerRootFSMount, "/etc/ld.so.cache")
	err = os.Remove(linkerCachePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("Failed to remove the ld.so.cache file: %w\n", err)
		}

		fmt.Printf("Linker cache not found in %q, skipping removal\n", linkerCachePath)
	}

	// Run `ldconfig` on the HOST (but targeting the container rootFS) to reduce the risk of running untrusted code in the container.
	err = exec.Command("/sbin/ldconfig", "-r", containerRootFSMount).Run()
	if err != nil {
		return fmt.Errorf("Failed to run ldconfig in the container rootfs: %w\n", err)
	}

	return nil
}

// Run executes the `lxd callhook` command.
func (c *cmdCallhook) Run(cmd *cobra.Command, args []string) error {
	// Only root should run this.
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Parse request.
	lxdPath, projectName, instanceRef, hook, cdiHooksFiles, err := callhook.ParseArgs(args)
	if err != nil {
		_ = cmd.Help()
		if len(args) == 0 {
			return nil
		}

		return err
	}

	// Handle startmountns hook.
	if hook == "startmountns" {
		if len(cdiHooksFiles) == 0 {
			return fmt.Errorf("Missing required CDI hooks files argument")
		}

		if c.devicesRootFolder == "" {
			return fmt.Errorf("Missing required --devicesRootFolder <directory> flag")
		}

		var err error
		for _, cdiHooksFile := range cdiHooksFiles {
			err = applyCDIHooksToContainer(c.devicesRootFolder, cdiHooksFile)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Handle all other hook types.
	return callhook.HandleContainerHook(lxdPath, projectName, instanceRef, hook)
}
