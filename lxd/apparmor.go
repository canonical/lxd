package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"

	log "github.com/lxc/lxd/shared/log15"
)

const (
	APPARMOR_CMD_LOAD   = "r"
	APPARMOR_CMD_UNLOAD = "R"
	APPARMOR_CMD_PARSE  = "Q"
)

var aaPath = shared.VarPath("security", "apparmor")

const AA_PROFILE_BASE = `
  ### Base profile
  capability,
  dbus,
  file,
  network,
  umount,

  # Allow us to receive signals from anywhere.
  signal (receive),

  # Allow us to send signals to ourselves
  signal peer=@{profile_name},

  # Allow other processes to read our /proc entries, futexes, perf tracing and
  # kcmp for now (they will need 'read' in the first place). Administrators can
  # override with:
  #   deny ptrace (readby) ...
  ptrace (readby),

  # Allow other processes to trace us by default (they will need 'trace' in
  # the first place). Administrators can override with:
  #   deny ptrace (tracedby) ...
  ptrace (tracedby),

  # Allow us to ptrace ourselves
  ptrace peer=@{profile_name},

  # ignore DENIED message on / remount
  deny mount options=(ro, remount) -> /,
  deny mount options=(ro, remount, silent) -> /,

  # allow tmpfs mounts everywhere
  mount fstype=tmpfs,

  # allow hugetlbfs mounts everywhere
  mount fstype=hugetlbfs,

  # allow mqueue mounts everywhere
  mount fstype=mqueue,

  # allow fuse mounts everywhere
  mount fstype=fuse,
  mount fstype=fuse.*,

  # deny access under /proc/bus to avoid e.g. messing with pci devices directly
  deny @{PROC}/bus/** wklx,

  # deny writes in /proc/sys/fs but allow binfmt_misc to be mounted
  mount fstype=binfmt_misc -> /proc/sys/fs/binfmt_misc/,
  deny @{PROC}/sys/fs/** wklx,

  # allow efivars to be mounted, writing to it will be blocked though
  mount fstype=efivarfs -> /sys/firmware/efi/efivars/,

  # block some other dangerous paths
  deny @{PROC}/kcore rwklx,
  deny @{PROC}/sysrq-trigger rwklx,

  # deny writes in /sys except for /sys/fs/cgroup, also allow
  # fusectl, securityfs and debugfs to be mounted there (read-only)
  mount fstype=fusectl -> /sys/fs/fuse/connections/,
  mount fstype=securityfs -> /sys/kernel/security/,
  mount fstype=debugfs -> /sys/kernel/debug/,
  deny mount fstype=debugfs -> /var/lib/ureadahead/debugfs/,
  mount fstype=proc -> /proc/,
  mount fstype=sysfs -> /sys/,
  mount options=(rw, nosuid, nodev, noexec, remount) -> /sys/,
  deny /sys/firmware/efi/efivars/** rwklx,
  # note, /sys/kernel/security/** handled below
  mount options=(move) /sys/fs/cgroup/cgmanager/ -> /sys/fs/cgroup/cgmanager.lower/,
  mount options=(ro, nosuid, nodev, noexec, remount, strictatime) -> /sys/fs/cgroup/,

  # deny reads from debugfs
  deny /sys/kernel/debug/{,**} rwklx,

  # allow paths to be made slave, shared, private or unbindable
  # FIXME: This currently doesn't work due to the apparmor parser treating those as allowing all mounts.
#  mount options=(rw,make-slave) -> **,
#  mount options=(rw,make-rslave) -> **,
#  mount options=(rw,make-shared) -> **,
#  mount options=(rw,make-rshared) -> **,
#  mount options=(rw,make-private) -> **,
#  mount options=(rw,make-rprivate) -> **,
#  mount options=(rw,make-unbindable) -> **,
#  mount options=(rw,make-runbindable) -> **,

  # allow bind-mounts of anything except /proc, /sys and /dev
  mount options=(rw,bind) /[^spd]*{,/**},
  mount options=(rw,bind) /d[^e]*{,/**},
  mount options=(rw,bind) /de[^v]*{,/**},
  mount options=(rw,bind) /dev/.[^l]*{,/**},
  mount options=(rw,bind) /dev/.l[^x]*{,/**},
  mount options=(rw,bind) /dev/.lx[^c]*{,/**},
  mount options=(rw,bind) /dev/.lxc?*{,/**},
  mount options=(rw,bind) /dev/[^.]*{,/**},
  mount options=(rw,bind) /dev?*{,/**},
  mount options=(rw,bind) /p[^r]*{,/**},
  mount options=(rw,bind) /pr[^o]*{,/**},
  mount options=(rw,bind) /pro[^c]*{,/**},
  mount options=(rw,bind) /proc?*{,/**},
  mount options=(rw,bind) /s[^y]*{,/**},
  mount options=(rw,bind) /sy[^s]*{,/**},
  mount options=(rw,bind) /sys?*{,/**},

  # allow read-only bind-mounts of anything except /proc, /sys and /dev
  mount options=(ro,remount,bind) -> /[^spd]*{,/**},
  mount options=(ro,remount,bind) -> /d[^e]*{,/**},
  mount options=(ro,remount,bind) -> /de[^v]*{,/**},
  mount options=(ro,remount,bind) -> /dev/.[^l]*{,/**},
  mount options=(ro,remount,bind) -> /dev/.l[^x]*{,/**},
  mount options=(ro,remount,bind) -> /dev/.lx[^c]*{,/**},
  mount options=(ro,remount,bind) -> /dev/.lxc?*{,/**},
  mount options=(ro,remount,bind) -> /dev/[^.]*{,/**},
  mount options=(ro,remount,bind) -> /dev?*{,/**},
  mount options=(ro,remount,bind) -> /p[^r]*{,/**},
  mount options=(ro,remount,bind) -> /pr[^o]*{,/**},
  mount options=(ro,remount,bind) -> /pro[^c]*{,/**},
  mount options=(ro,remount,bind) -> /proc?*{,/**},
  mount options=(ro,remount,bind) -> /s[^y]*{,/**},
  mount options=(ro,remount,bind) -> /sy[^s]*{,/**},
  mount options=(ro,remount,bind) -> /sys?*{,/**},

  # allow moving mounts except for /proc, /sys and /dev
  mount options=(rw,move) /[^spd]*{,/**},
  mount options=(rw,move) /d[^e]*{,/**},
  mount options=(rw,move) /de[^v]*{,/**},
  mount options=(rw,move) /dev/.[^l]*{,/**},
  mount options=(rw,move) /dev/.l[^x]*{,/**},
  mount options=(rw,move) /dev/.lx[^c]*{,/**},
  mount options=(rw,move) /dev/.lxc?*{,/**},
  mount options=(rw,move) /dev/[^.]*{,/**},
  mount options=(rw,move) /dev?*{,/**},
  mount options=(rw,move) /p[^r]*{,/**},
  mount options=(rw,move) /pr[^o]*{,/**},
  mount options=(rw,move) /pro[^c]*{,/**},
  mount options=(rw,move) /proc?*{,/**},
  mount options=(rw,move) /s[^y]*{,/**},
  mount options=(rw,move) /sy[^s]*{,/**},
  mount options=(rw,move) /sys?*{,/**},

  # generated by: lxc-generate-aa-rules.py container-rules.base
  deny /proc/sys/[^kn]*{,/**} wklx,
  deny /proc/sys/k[^e]*{,/**} wklx,
  deny /proc/sys/ke[^r]*{,/**} wklx,
  deny /proc/sys/ker[^n]*{,/**} wklx,
  deny /proc/sys/kern[^e]*{,/**} wklx,
  deny /proc/sys/kerne[^l]*{,/**} wklx,
  deny /proc/sys/kernel/[^smhd]*{,/**} wklx,
  deny /proc/sys/kernel/d[^o]*{,/**} wklx,
  deny /proc/sys/kernel/do[^m]*{,/**} wklx,
  deny /proc/sys/kernel/dom[^a]*{,/**} wklx,
  deny /proc/sys/kernel/doma[^i]*{,/**} wklx,
  deny /proc/sys/kernel/domai[^n]*{,/**} wklx,
  deny /proc/sys/kernel/domain[^n]*{,/**} wklx,
  deny /proc/sys/kernel/domainn[^a]*{,/**} wklx,
  deny /proc/sys/kernel/domainna[^m]*{,/**} wklx,
  deny /proc/sys/kernel/domainnam[^e]*{,/**} wklx,
  deny /proc/sys/kernel/domainname?*{,/**} wklx,
  deny /proc/sys/kernel/h[^o]*{,/**} wklx,
  deny /proc/sys/kernel/ho[^s]*{,/**} wklx,
  deny /proc/sys/kernel/hos[^t]*{,/**} wklx,
  deny /proc/sys/kernel/host[^n]*{,/**} wklx,
  deny /proc/sys/kernel/hostn[^a]*{,/**} wklx,
  deny /proc/sys/kernel/hostna[^m]*{,/**} wklx,
  deny /proc/sys/kernel/hostnam[^e]*{,/**} wklx,
  deny /proc/sys/kernel/hostname?*{,/**} wklx,
  deny /proc/sys/kernel/m[^s]*{,/**} wklx,
  deny /proc/sys/kernel/ms[^g]*{,/**} wklx,
  deny /proc/sys/kernel/msg*/** wklx,
  deny /proc/sys/kernel/s[^he]*{,/**} wklx,
  deny /proc/sys/kernel/se[^m]*{,/**} wklx,
  deny /proc/sys/kernel/sem*/** wklx,
  deny /proc/sys/kernel/sh[^m]*{,/**} wklx,
  deny /proc/sys/kernel/shm*/** wklx,
  deny /proc/sys/kernel?*{,/**} wklx,
  deny /proc/sys/n[^e]*{,/**} wklx,
  deny /proc/sys/ne[^t]*{,/**} wklx,
  deny /proc/sys/net?*{,/**} wklx,
  deny /sys/[^fdck]*{,/**} wklx,
  deny /sys/c[^l]*{,/**} wklx,
  deny /sys/cl[^a]*{,/**} wklx,
  deny /sys/cla[^s]*{,/**} wklx,
  deny /sys/clas[^s]*{,/**} wklx,
  deny /sys/class/[^n]*{,/**} wklx,
  deny /sys/class/n[^e]*{,/**} wklx,
  deny /sys/class/ne[^t]*{,/**} wklx,
  deny /sys/class/net?*{,/**} wklx,
  deny /sys/class?*{,/**} wklx,
  deny /sys/d[^e]*{,/**} wklx,
  deny /sys/de[^v]*{,/**} wklx,
  deny /sys/dev[^i]*{,/**} wklx,
  deny /sys/devi[^c]*{,/**} wklx,
  deny /sys/devic[^e]*{,/**} wklx,
  deny /sys/device[^s]*{,/**} wklx,
  deny /sys/devices/[^v]*{,/**} wklx,
  deny /sys/devices/v[^i]*{,/**} wklx,
  deny /sys/devices/vi[^r]*{,/**} wklx,
  deny /sys/devices/vir[^t]*{,/**} wklx,
  deny /sys/devices/virt[^u]*{,/**} wklx,
  deny /sys/devices/virtu[^a]*{,/**} wklx,
  deny /sys/devices/virtua[^l]*{,/**} wklx,
  deny /sys/devices/virtual/[^n]*{,/**} wklx,
  deny /sys/devices/virtual/n[^e]*{,/**} wklx,
  deny /sys/devices/virtual/ne[^t]*{,/**} wklx,
  deny /sys/devices/virtual/net?*{,/**} wklx,
  deny /sys/devices/virtual?*{,/**} wklx,
  deny /sys/devices?*{,/**} wklx,
  deny /sys/f[^s]*{,/**} wklx,
  deny /sys/fs/[^c]*{,/**} wklx,
  deny /sys/fs/c[^g]*{,/**} wklx,
  deny /sys/fs/cg[^r]*{,/**} wklx,
  deny /sys/fs/cgr[^o]*{,/**} wklx,
  deny /sys/fs/cgro[^u]*{,/**} wklx,
  deny /sys/fs/cgrou[^p]*{,/**} wklx,
  deny /sys/fs/cgroup?*{,/**} wklx,
  deny /sys/fs?*{,/**} wklx,
`

const AA_PROFILE_NESTING = `
  pivot_root,
  ptrace,
  signal,

  deny /dev/.lxc/proc/** rw,
  deny /dev/.lxc/sys/** rw,

  mount /var/lib/lxd/shmounts/ -> /var/lib/lxd/shmounts/,
  mount none -> /var/lib/lxd/shmounts/,
  mount fstype=proc -> /usr/lib/*/lxc/**,
  mount fstype=sysfs -> /usr/lib/*/lxc/**,
  mount options=(rw,bind),
  mount options=(rw,rbind),
  mount options=(rw,make-rshared),

  # there doesn't seem to be a way to ask for:
  # mount options=(ro,nosuid,nodev,noexec,remount,bind),
  # as we always get mount to $cdir/proc/sys with those flags denied
  # So allow all mounts until that is straightened out:
  mount,
  mount options=bind /var/lib/lxd/shmounts/** -> /var/lib/lxd/**,
`

const AA_PROFILE_UNPRIVILEGED = `
  pivot_root,

  # Allow modifying mount propagation
  mount options=(rw,make-slave) -> **,
  mount options=(rw,make-rslave) -> **,
  mount options=(rw,make-shared) -> **,
  mount options=(rw,make-rshared) -> **,
  mount options=(rw,make-private) -> **,
  mount options=(rw,make-rprivate) -> **,
  mount options=(rw,make-unbindable) -> **,
  mount options=(rw,make-runbindable) -> **,

  # Allow all bind-mounts
  mount options=(rw,bind),
  mount options=(rw,rbind),

  # Allow remounting things read-only
  mount options=(ro,remount),
`

func mkApparmorName(name string) string {
	if len(name)+7 >= 253 {
		hash := sha256.New()
		io.WriteString(hash, name)
		return fmt.Sprintf("%x", hash.Sum(nil))
	}

	return name
}

func AANamespace(c container) string {
	/* / is not allowed in apparmor namespace names; let's also trim the
	 * leading / so it doesn't look like "-var-lib-lxd"
	 */
	lxddir := strings.Replace(strings.Trim(shared.VarPath(""), "/"), "/", "-", -1)
	lxddir = mkApparmorName(lxddir)
	name := projectPrefix(c.Project(), c.Name())
	return fmt.Sprintf("lxd-%s_<%s>", name, lxddir)
}

func AAProfileFull(c container) string {
	lxddir := shared.VarPath("")
	lxddir = mkApparmorName(lxddir)
	name := projectPrefix(c.Project(), c.Name())
	return fmt.Sprintf("lxd-%s_<%s>", name, lxddir)
}

func AAProfileShort(c container) string {
	name := projectPrefix(c.Project(), c.Name())
	return fmt.Sprintf("lxd-%s", name)
}

// getProfileContent generates the apparmor profile template from the given
// container. This includes the stock lxc includes as well as stuff from
// raw.apparmor.
func getAAProfileContent(c container) string {
	profile := strings.TrimLeft(AA_PROFILE_BASE, "\n")

	// Apply new features
	if aaParserSupports("unix") {
		profile += `
  ### Feature: unix
  # Allow receive via unix sockets from anywhere
  unix (receive),

  # Allow all unix in the container
  unix peer=(label=@{profile_name}),
`
	}

	// Apply cgns bits
	if shared.PathExists("/proc/self/ns/cgroup") {
		profile += "\n  ### Feature: cgroup namespace\n"
		profile += "  mount fstype=cgroup -> /sys/fs/cgroup/**,\n"
		profile += "  mount fstype=cgroup2 -> /sys/fs/cgroup/**,\n"
	}

	state := c.DaemonState()
	if state.OS.AppArmorStacking && !state.OS.AppArmorStacked {
		profile += "\n  ### Feature: apparmor stacking\n"
		profile += `  ### Configuration: apparmor profile loading (in namespace)
  deny /sys/k[^e]*{,/**} wklx,
  deny /sys/ke[^r]*{,/**} wklx,
  deny /sys/ker[^n]*{,/**} wklx,
  deny /sys/kern[^e]*{,/**} wklx,
  deny /sys/kerne[^l]*{,/**} wklx,
  deny /sys/kernel/[^s]*{,/**} wklx,
  deny /sys/kernel/s[^e]*{,/**} wklx,
  deny /sys/kernel/se[^c]*{,/**} wklx,
  deny /sys/kernel/sec[^u]*{,/**} wklx,
  deny /sys/kernel/secu[^r]*{,/**} wklx,
  deny /sys/kernel/secur[^i]*{,/**} wklx,
  deny /sys/kernel/securi[^t]*{,/**} wklx,
  deny /sys/kernel/securit[^y]*{,/**} wklx,
  deny /sys/kernel/security/[^a]*{,/**} wklx,
  deny /sys/kernel/security/a[^p]*{,/**} wklx,
  deny /sys/kernel/security/ap[^p]*{,/**} wklx,
  deny /sys/kernel/security/app[^a]*{,/**} wklx,
  deny /sys/kernel/security/appa[^r]*{,/**} wklx,
  deny /sys/kernel/security/appar[^m]*{,/**} wklx,
  deny /sys/kernel/security/apparm[^o]*{,/**} wklx,
  deny /sys/kernel/security/apparmo[^r]*{,/**} wklx,
  deny /sys/kernel/security/apparmor?*{,/**} wklx,
  deny /sys/kernel/security?*{,/**} wklx,
  deny /sys/kernel?*{,/**} wklx,
`
		profile += fmt.Sprintf("  change_profile -> \":%s:*\",\n", AANamespace(c))
		profile += fmt.Sprintf("  change_profile -> \":%s://*\",\n", AANamespace(c))
	} else {
		profile += "\n  ### Feature: apparmor stacking (not present)\n"
		profile += "  deny /sys/k*{,/**} rwklx,\n"
	}

	if c.IsNesting() {
		// Apply nesting bits
		profile += "\n  ### Configuration: nesting\n"
		profile += strings.TrimLeft(AA_PROFILE_NESTING, "\n")
		if !state.OS.AppArmorStacking || state.OS.AppArmorStacked {
			profile += fmt.Sprintf("  change_profile -> \"%s\",\n", AAProfileFull(c))
		}
	}

	if !c.IsPrivileged() || state.OS.RunningInUserNS {
		// Apply unprivileged bits
		profile += "\n  ### Configuration: unprivileged containers\n"
		profile += strings.TrimLeft(AA_PROFILE_UNPRIVILEGED, "\n")
	}

	// Append raw.apparmor
	rawApparmor, ok := c.ExpandedConfig()["raw.apparmor"]
	if ok {
		profile += "\n  ### Configuration: raw.apparmor\n"
		for _, line := range strings.Split(strings.Trim(rawApparmor, "\n"), "\n") {
			profile += fmt.Sprintf("  %s\n", line)
		}
	}

	return fmt.Sprintf(`#include <tunables/global>
profile "%s" flags=(attach_disconnected,mediate_deleted) {
%s
}
`, AAProfileFull(c), strings.Trim(profile, "\n"))
}

func runApparmor(command string, c container) error {
	state := c.DaemonState()
	if !state.OS.AppArmorAvailable {
		return nil
	}

	output, err := shared.RunCommand("apparmor_parser", []string{
		fmt.Sprintf("-%sWL", command),
		path.Join(aaPath, "cache"),
		path.Join(aaPath, "profiles", AAProfileShort(c)),
	}...)

	if err != nil {
		logger.Error("Running apparmor",
			log.Ctx{"action": command, "output": output, "err": err})
	}

	return err
}

func getAACacheDir() string {
	basePath := path.Join(aaPath, "cache")

	major, minor, _, err := getAAParserVersion()
	if err != nil {
		return basePath
	}

	// multiple policy cache directories were only added in v2.13
	if major < 2 || (major == 2 && minor < 13) {
		return basePath
	}

	output, err := shared.RunCommand("apparmor_parser", "-L", basePath, "--print-cache-dir")
	if err != nil {
		return basePath
	}

	return strings.TrimSpace(output)
}

func mkApparmorNamespace(c container, namespace string) error {
	state := c.DaemonState()
	if !state.OS.AppArmorStacking || state.OS.AppArmorStacked {
		return nil
	}

	p := path.Join("/sys/kernel/security/apparmor/policy/namespaces", namespace)
	if err := os.Mkdir(p, 0755); !os.IsExist(err) {
		return err
	}

	return nil
}

// Ensure that the container's policy is loaded into the kernel so the
// container can boot.
func AALoadProfile(c container) error {
	state := c.DaemonState()
	if !state.OS.AppArmorAdmin {
		return nil
	}

	if err := mkApparmorNamespace(c, AANamespace(c)); err != nil {
		return err
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

// Ensure that the container's policy namespace is unloaded to free kernel
// memory. This does not delete the policy from disk or cache.
func AADestroy(c container) error {
	state := c.DaemonState()
	if !state.OS.AppArmorAdmin {
		return nil
	}

	if state.OS.AppArmorStacking && !state.OS.AppArmorStacked {
		p := path.Join("/sys/kernel/security/apparmor/policy/namespaces", AANamespace(c))
		if err := os.Remove(p); err != nil {
			logger.Error("Error removing apparmor namespace", log.Ctx{"err": err, "ns": p})
		}
	}

	return runApparmor(APPARMOR_CMD_UNLOAD, c)
}

// Parse the profile without loading it into the kernel.
func AAParseProfile(c container) error {
	state := c.DaemonState()
	if !state.OS.AppArmorAvailable {
		return nil
	}

	return runApparmor(APPARMOR_CMD_PARSE, c)
}

// Delete the policy from cache/disk.
func AADeleteProfile(c container) {
	state := c.DaemonState()
	if !state.OS.AppArmorAdmin {
		return
	}

	/* It's ok if these deletes fail: if the container was never started,
	 * we'll have never written a profile or cached it.
	 */
	os.Remove(path.Join(getAACacheDir(), AAProfileShort(c)))
	os.Remove(path.Join(aaPath, "profiles", AAProfileShort(c)))
}

func aaParserSupports(feature string) bool {
	major, minor, micro, err := getAAParserVersion()
	if err != nil {
		return false
	}

	switch feature {
	case "unix":
		if major < 2 {
			return false
		}

		if major == 2 && minor < 10 {
			return false
		}

		if major == 2 && minor == 10 && micro < 95 {
			return false
		}
	}

	return true
}

func getAAParserVersion() (major int, minor int, micro int, err error) {
	var out string

	out, err = shared.RunCommand("apparmor_parser", "--version")
	if err != nil {
		return
	}

	_, err = fmt.Sscanf(strings.Split(out, "\n")[0], "AppArmor parser version %d.%d.%d", &major, &minor, &micro)

	return
}
