package main

import (
	"io/ioutil"
	"os"
	"path"

	"github.com/lxc/lxd/shared"
)

const DEFAULT_SECCOMP_POLICY = `
2
blacklist
reject_force_umount  # comment this to allow umount -f;  not recommended
[all]
kexec_load errno 1
open_by_handle_at errno 1
init_module errno 1
finit_module errno 1
delete_module errno 1
`

var seccompPath = shared.VarPath("security", "seccomp")

func SeccompProfilePath(c *containerLXD) string {
	return path.Join(seccompPath, c.name)
}

func getSeccompProfileContent(c *containerLXD) string {
	/* for now there are no seccomp knobs. */
	return DEFAULT_SECCOMP_POLICY
}

func SeccompCreateProfile(c *containerLXD) error {
	/* Unlike apparmor, there is no way to "cache" profiles, and profiles
	 * are automatically unloaded when a task dies. Thus, we don't need to
	 * unload them when a container stops, and we don't have to worry about
	 * the mtime on the file for any compiler purpose, so let's just write
	 * out the profile.
	 */
	profile := getSeccompProfileContent(c)
	if err := os.MkdirAll(seccompPath, 0700); err != nil {
		return err
	}

	return ioutil.WriteFile(SeccompProfilePath(c), []byte(profile), 0600)
}

func SeccompDeleteProfile(c *containerLXD) {
	/* similar to AppArmor, if we've never started this container, the
	 * delete can fail and that's ok.
	 */
	os.Remove(SeccompProfilePath(c))
}
