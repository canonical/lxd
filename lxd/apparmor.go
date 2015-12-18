package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

const (
	APPARMOR_CMD_LOAD   = "r"
	APPARMOR_CMD_UNLOAD = "R"
	APPARMOR_CMD_PARSE  = "Q"
)

var aaPath = shared.VarPath("security", "apparmor")

const NESTING_AA_PROFILE = `
  pivot_root,
  mount /var/lib/lxd/shmounts/ -> /var/lib/lxd/shmounts/,
  mount none -> /var/lib/lxd/shmounts/,
  mount fstype=proc -> /usr/lib/x86_64-linux-gnu/lxc/**,
  mount fstype=sysfs -> /usr/lib/x86_64-linux-gnu/lxc/**,
  mount options=(rw,bind),
  mount options=(rw,rbind),
  deny /dev/.lxd/proc/** rw,
  deny /dev/.lxd/sys/** rw,
  mount options=(rw,make-rshared),

  # there doesn't seem to be a way to ask for:
  # mount options=(ro,nosuid,nodev,noexec,remount,bind),
  # as we always get mount to $cdir/proc/sys with those flags denied
  # So allow all mounts until that is straightened out:
  mount,
  mount options=bind /var/lib/lxd/shmounts/** -> /var/lib/lxd/**,
  # lxc-container-default-with-nesting also inherited these
  # from start-container, and seems to need them.
  ptrace,
  signal,
`

const DEFAULT_AA_PROFILE = `
#include <tunables/global>
profile "%s" flags=(attach_disconnected,mediate_deleted) {
    #include <abstractions/lxc/container-base>

    # user input raw.apparmor below here
    %s

    # nesting support goes here if needed
    %s
    change_profile -> "%s",
}`

func AAProfileFull(c container) string {
	lxddir := shared.VarPath("")
	if len(c.Name())+len(lxddir)+7 >= 253 {
		hash := sha256.New()
		io.WriteString(hash, lxddir)
		lxddir = fmt.Sprintf("%x", hash.Sum(nil))
	}

	return fmt.Sprintf("lxd-%s_<%s>", c.Name(), lxddir)
}

func AAProfileShort(c container) string {
	return fmt.Sprintf("lxd-%s", c.Name())
}

// getProfileContent generates the apparmor profile template from the given
// container. This includes the stock lxc includes as well as stuff from
// raw.apparmor.
func getAAProfileContent(c container) string {
	rawApparmor, ok := c.ExpandedConfig()["raw.apparmor"]
	if !ok {
		rawApparmor = ""
	}

	nesting := ""
	if c.IsNesting() {
		nesting = NESTING_AA_PROFILE
	}

	return fmt.Sprintf(DEFAULT_AA_PROFILE, AAProfileFull(c), rawApparmor, nesting, AAProfileFull(c))
}

func runApparmor(command string, c container) error {
	if !aaAvailable {
		return nil
	}

	cmd := exec.Command("apparmor_parser", []string{
		fmt.Sprintf("-%sWL", command),
		path.Join(aaPath, "cache"),
		path.Join(aaPath, "profiles", AAProfileShort(c)),
	}...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		shared.Log.Error("Running apparmor",
			log.Ctx{"action": command, "output": string(output), "err": err})
	}

	return err
}

// Ensure that the container's policy is loaded into the kernel so the
// container can boot.
func AALoadProfile(c container) error {
	if !aaAdmin {
		return nil
	}

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
	profile := path.Join(aaPath, "profiles", AAProfileShort(c))
	content, err := ioutil.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	updated := getAAProfileContent(c)

	if string(content) != string(updated) {
		if err := os.MkdirAll(path.Join(aaPath, "cache"), 0700); err != nil {
			return err
		}

		if err := os.MkdirAll(path.Join(aaPath, "profiles"), 0700); err != nil {
			return err
		}

		if err := ioutil.WriteFile(profile, []byte(updated), 0600); err != nil {
			return err
		}
	}

	return runApparmor(APPARMOR_CMD_LOAD, c)
}

// Ensure that the container's policy is unloaded to free kernel memory. This
// does not delete the policy from disk or cache.
func AAUnloadProfile(c container) error {
	if !aaAdmin {
		return nil
	}

	return runApparmor(APPARMOR_CMD_UNLOAD, c)
}

// Parse the profile without loading it into the kernel.
func AAParseProfile(c container) error {
	if !aaAvailable {
	}

	return runApparmor(APPARMOR_CMD_PARSE, c)
}

// Delete the policy from cache/disk.
func AADeleteProfile(c container) {
	if !aaAdmin {
		return
	}

	/* It's ok if these deletes fail: if the container was never started,
	 * we'll have never written a profile or cached it.
	 */
	os.Remove(path.Join(aaPath, "cache", AAProfileShort(c)))
	os.Remove(path.Join(aaPath, "profiles", AAProfileShort(c)))
}

// What's current apparmor profile
func aaProfile() string {
	contents, err := ioutil.ReadFile("/proc/self/attr/current")
	if err == nil {
		return strings.TrimSpace(string(contents))
	}
	return ""
}
