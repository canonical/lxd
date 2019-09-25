package instance

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

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

const SECCOMP_NOTIFY_MKNOD = `mknod notify [1,8192,SCMP_CMP_MASKED_EQ,61440]
mknod notify [1,24576,SCMP_CMP_MASKED_EQ,61440]
mknodat notify [2,8192,SCMP_CMP_MASKED_EQ,61440]
mknodat notify [2,24576,SCMP_CMP_MASKED_EQ,61440]
`

const SECCOMP_NOTIFY_SETXATTR = `setxattr notify [3,1,SCMP_CMP_EQ]
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

var SeccompPath = shared.VarPath("security", "seccomp")

func SeccompProfilePath(c Instance) string {
	return path.Join(SeccompPath, c.Name())
}

func SeccompContainerNeedsPolicy(c Instance) bool {
	config := c.ExpandedConfig()

	// Check for text keys
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

	// Check for boolean keys that default to false
	keys = []string{
		"security.syscalls.blacklist_compat",
		"security.syscalls.intercept.mknod",
		"security.syscalls.intercept.setxattr",
	}

	for _, k := range keys {
		if shared.IsTrue(config[k]) {
			return true
		}
	}

	// Check for boolean keys that default to true
	keys = []string{
		"security.syscalls.blacklist_default",
	}

	for _, k := range keys {
		value, ok := config[k]
		if !ok || shared.IsTrue(value) {
			return true
		}
	}

	return false
}

func SeccompContainerNeedsIntercept(c Instance) (bool, error) {
	// No need if privileged
	if c.IsPrivileged() {
		return false, nil
	}

	// If nested, assume the host handles it
	if c.DaemonState().OS.RunningInUserNS {
		return false, nil
	}

	config := c.ExpandedConfig()

	keys := []string{
		"security.syscalls.intercept.mknod",
		"security.syscalls.intercept.setxattr",
	}

	needed := false
	for _, k := range keys {
		if shared.IsTrue(config[k]) {
			needed = true
			break
		}
	}

	if needed {
		if !lxcSupportSeccompNotify(c.DaemonState()) {
			return needed, fmt.Errorf("System doesn't support syscall interception")
		}
	}

	return needed, nil
}

func SeccompCreateProfile(c Instance) error {
	/* Unlike apparmor, there is no way to "cache" profiles, and profiles
	 * are automatically unloaded when a task dies. Thus, we don't need to
	 * unload them when a container stops, and we don't have to worry about
	 * the mtime on the file for any compiler purpose, so let's just write
	 * out the profile.
	 */
	if !SeccompContainerNeedsPolicy(c) {
		return nil
	}

	profile, err := SeccompGetPolicyContent(c)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(SeccompPath, 0700); err != nil {
		return err
	}

	return ioutil.WriteFile(SeccompProfilePath(c), []byte(profile), 0600)
}

func SeccompGetPolicyContent(c Instance) (string, error) {
	config := c.ExpandedConfig()

	// Full policy override
	raw := config["raw.seccomp"]
	if raw != "" {
		return raw, nil
	}

	// Policy header
	policy := SECCOMP_HEADER
	whitelist := config["security.syscalls.whitelist"]
	if whitelist != "" {
		policy += "whitelist\n[all]\n"
		policy += whitelist
	} else {
		policy += "blacklist\n"

		default_, ok := config["security.syscalls.blacklist_default"]
		if !ok || shared.IsTrue(default_) {
			policy += DEFAULT_SECCOMP_POLICY
		}
	}

	// Syscall interception
	ok, err := SeccompContainerNeedsIntercept(c)
	if err != nil {
		return "", err
	}

	if ok {
		if shared.IsTrue(config["security.syscalls.intercept.mknod"]) {
			policy += SECCOMP_NOTIFY_MKNOD
		}

		if shared.IsTrue(config["security.syscalls.intercept.setxattr"]) {
			policy += SECCOMP_NOTIFY_SETXATTR
		}
	}

	if whitelist != "" {
		return policy, nil
	}

	// Additional blacklist entries
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

func SeccompDeleteProfile(c Instance) {
	/* similar to AppArmor, if we've never started this container, the
	 * delete can fail and that's ok.
	 */
	os.Remove(SeccompProfilePath(c))
}

func TaskIDs(pid int) (error, int64, int64, int64, int64) {
	status, err := ioutil.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return err, -1, -1, -1, -1
	}

	reUid := regexp.MustCompile("Uid:\\s*([0-9]*)\\s*([0-9]*)\\s*([0-9]*)\\s*([0-9]*)")
	reGid := regexp.MustCompile("Gid:\\s*([0-9]*)\\s*([0-9]*)\\s*([0-9]*)\\s*([0-9]*)")
	var gid int64 = -1
	var uid int64 = -1
	var fsgid int64 = -1
	var fsuid int64 = -1
	uidFound := false
	gidFound := false
	for _, line := range strings.Split(string(status), "\n") {
		if uidFound && gidFound {
			break
		}

		if !uidFound {
			m := reUid.FindStringSubmatch(line)
			if m != nil && len(m) > 2 {
				// effective uid
				result, err := strconv.ParseInt(m[2], 10, 64)
				if err != nil {
					return err, -1, -1, -1, -1
				}

				uid = result
				uidFound = true
			}

			if m != nil && len(m) > 4 {
				// fsuid
				result, err := strconv.ParseInt(m[4], 10, 64)
				if err != nil {
					return err, -1, -1, -1, -1
				}

				fsuid = result
			}

			continue
		}

		if !gidFound {
			m := reGid.FindStringSubmatch(line)
			if m != nil && len(m) > 2 {
				// effective gid
				result, err := strconv.ParseInt(m[2], 10, 64)
				if err != nil {
					return err, -1, -1, -1, -1
				}

				gid = result
				gidFound = true
			}

			if m != nil && len(m) > 4 {
				// fsgid
				result, err := strconv.ParseInt(m[4], 10, 64)
				if err != nil {
					return err, -1, -1, -1, -1
				}

				fsgid = result
			}

			continue
		}
	}

	return nil, uid, gid, fsuid, fsgid
}
