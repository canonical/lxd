package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

const (
	APPARMOR_CMD_LOAD   = "r"
	APPARMOR_CMD_UNLOAD = "R"
)

var aaPath = shared.VarPath("security", "apparmor")

const DEFAULT_POLICY = `
#include <tunables/global>
profile lxd-%s flags=(attach_disconnected,mediate_deleted) {
    #include <abstractions/lxc/container-base>

    # user input raw.apparmor below here
    %s
}`

func AAProfileName(c *containerLXD) string {
	return fmt.Sprintf("lxd-%s", c.name)
}

// getProfileContent generates the apparmor profile template from the given
// container. This includes the stock lxc includes as well as stuff from
// raw.apparmor.
func getProfileContent(c *containerLXD) string {
	rawApparmor, ok := c.config["raw.apparmor"]
	if !ok {
		rawApparmor = ""
	}

	return fmt.Sprintf(DEFAULT_POLICY, c.name, rawApparmor)
}

func runApparmor(command string, profile string) error {
	cmd := exec.Command("apparmor_parser", []string{
		fmt.Sprintf("-%sWL", command),
		path.Join(aaPath, "cache"),
		path.Join(aaPath, "profiles", profile),
	}...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		shared.Log.Error("Running apparmor",
			log.Ctx{"output": string(output), "err": err})
	}

	return err
}

// Ensure that the container's policy is loaded into the kernel so the
// container can boot.
func AALoadProfile(c *containerLXD) error {
	/* In order to avoid forcing a profile parse (potentially slow) on
	 * every container start, let's use apparmor's binary policy cache,
	 * which checks mtime of the files to figure out if the policy needs to
	 * be regenerated.
	 *
	 * Since it uses mtimes, we shouldn't just always write out our local
	 * apparmor template; instead we should check to see whether the
	 * template is the same as ours. If it isn't we should write our
	 * version out so that the new changes are reflected and we definitely
	 * force a recompile.
	 */
	profile := path.Join(aaPath, "profiles", AAProfileName(c))
	content, err := ioutil.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	updated := getProfileContent(c)

	if string(content) != string(updated) {
		if err := os.MkdirAll(path.Join(aaPath, "profiles"), 0700); err != nil {
			return err
		}

		if err := ioutil.WriteFile(profile, []byte(updated), 0600); err != nil {
			return err
		}
	}

	return runApparmor(APPARMOR_CMD_LOAD, AAProfileName(c))
}

// Ensure that the container's policy is unloaded to free kernel memory. This
// does not delete the policy from disk or cache.
func AAUnloadProfile(c *containerLXD) error {
	return runApparmor(APPARMOR_CMD_UNLOAD, AAProfileName(c))
}

// Delete the policy from cache/disk.
func AADeleteProfile(c *containerLXD) {
	/* It's ok if these deletes fail: if the container was never started,
	 * we'll have never written a profile or cached it.
	 */
	os.Remove(path.Join(aaPath, "cache", AAProfileName(c)))
	os.Remove(path.Join(aaPath, "profiles", AAProfileName(c)))
}
