package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/osarch"
)

const SECCOMP_HEADER = `2
`

const DEFAULT_SECCOMP_POLICY = `reject_force_umount  # comment this to allow umount -f;  not recommended
[all]
kexec_load errno 38
open_by_handle_at errno 38
init_module errno 38
finit_module errno 38
delete_module errno 38
`
const COMPAT_BLOCKING_POLICY = `[%s]
compat_sys_rt_sigaction errno 38
stub_x32_rt_sigreturn errno 38
compat_sys_ioctl errno 38
compat_sys_readv errno 38
compat_sys_writev errno 38
compat_sys_recvfrom errno 38
compat_sys_sendmsg errno 38
compat_sys_recvmsg errno 38
stub_x32_execve errno 38
compat_sys_ptrace errno 38
compat_sys_rt_sigpending errno 38
compat_sys_rt_sigtimedwait errno 38
compat_sys_rt_sigqueueinfo errno 38
compat_sys_sigaltstack errno 38
compat_sys_timer_create errno 38
compat_sys_mq_notify errno 38
compat_sys_kexec_load errno 38
compat_sys_waitid errno 38
compat_sys_set_robust_list errno 38
compat_sys_get_robust_list errno 38
compat_sys_vmsplice errno 38
compat_sys_move_pages errno 38
compat_sys_preadv64 errno 38
compat_sys_pwritev64 errno 38
compat_sys_rt_tgsigqueueinfo errno 38
compat_sys_recvmmsg errno 38
compat_sys_sendmmsg errno 38
compat_sys_process_vm_readv errno 38
compat_sys_process_vm_writev errno 38
compat_sys_setsockopt errno 38
compat_sys_getsockopt errno 38
compat_sys_io_setup errno 38
compat_sys_io_submit errno 38
stub_x32_execveat errno 38
`

var seccompPath = shared.VarPath("security", "seccomp")

func SeccompProfilePath(c container) string {
	return path.Join(seccompPath, c.Name())
}

func ContainerNeedsSeccomp(c container) bool {
	config := c.ExpandedConfig()

	keys := []string{
		"raw.seccomp",
		"security.syscalls.whitelist",
		"security.syscalls.blacklist",
	}

	for _, k := range keys {
		_, hasKey := config[k]
		if hasKey {
			return true
		}
	}

	compat := config["security.syscalls.blacklist_compat"]
	if shared.IsTrue(compat) {
		return true
	}

	/* this are enabled by default, so if the keys aren't present, that
	 * means "true"
	 */
	default_, ok := config["security.syscalls.blacklist_default"]
	if !ok || shared.IsTrue(default_) {
		return true
	}

	return false
}

func getSeccompProfileContent(c container) (string, error) {
	config := c.ExpandedConfig()

	raw := config["raw.seccomp"]
	if raw != "" {
		return raw, nil
	}

	policy := SECCOMP_HEADER

	whitelist := config["security.syscalls.whitelist"]
	if whitelist != "" {
		policy += "whitelist\n[all]\n"
		policy += whitelist
		return policy, nil
	}

	policy += "blacklist\n"

	default_, ok := config["security.syscalls.blacklist_default"]
	if !ok || shared.IsTrue(default_) {
		policy += DEFAULT_SECCOMP_POLICY
	}

	compat := config["security.syscalls.blacklist_compat"]
	if shared.IsTrue(compat) {
		arch, err := osarch.ArchitectureName(c.Architecture())
		if err != nil {
			return "", err
		}
		policy += fmt.Sprintf(COMPAT_BLOCKING_POLICY, arch)
	}

	blacklist := config["security.syscalls.blacklist"]
	if blacklist != "" {
		policy += blacklist
	}

	return policy, nil
}

func SeccompCreateProfile(c container) error {
	/* Unlike apparmor, there is no way to "cache" profiles, and profiles
	 * are automatically unloaded when a task dies. Thus, we don't need to
	 * unload them when a container stops, and we don't have to worry about
	 * the mtime on the file for any compiler purpose, so let's just write
	 * out the profile.
	 */
	if !ContainerNeedsSeccomp(c) {
		return nil
	}

	profile, err := getSeccompProfileContent(c)
	if err != nil {
		return nil
	}

	if err := os.MkdirAll(seccompPath, 0700); err != nil {
		return err
	}

	return ioutil.WriteFile(SeccompProfilePath(c), []byte(profile), 0600)
}

func SeccompDeleteProfile(c container) {
	/* similar to AppArmor, if we've never started this container, the
	 * delete can fail and that's ok.
	 */
	os.Remove(SeccompProfilePath(c))
}
