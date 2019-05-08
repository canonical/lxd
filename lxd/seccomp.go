// +build linux
// +build cgo

package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path"
	"strconv"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/netutils"
	"github.com/lxc/lxd/shared/osarch"
)

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <errno.h>
#include <fcntl.h>
#include <linux/seccomp.h>
#include <linux/types.h>
#include <linux/kdev_t.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/sysmacros.h>
#include <sys/types.h>
#include <unistd.h>

struct seccomp_notify_proxy_msg {
	uint32_t version;
#ifdef SECCOMP_RET_USER_NOTIF
	struct seccomp_notif req;
	struct seccomp_notif_resp resp;
#endif // SECCOMP_RET_USER_NOTIF
	pid_t monitor_pid;
	pid_t init_pid;
};

#define SECCOMP_PROXY_MSG_SIZE (sizeof(struct seccomp_notify_proxy_msg))

#ifdef SECCOMP_RET_USER_NOTIF

static int device_allowed(dev_t dev, mode_t mode)
{
	if ((dev == makedev(5, 1)) && (mode & S_IFCHR)) // /dev/console
		return 0;

	if ((dev == makedev(1, 7)) && (mode & S_IFCHR)) // /dev/full
		return 0;

	if ((dev == makedev(1, 3)) && (mode & S_IFCHR)) // /dev/null
		return 0;

	if ((dev == makedev(1, 8)) && (mode & S_IFCHR)) // /dev/random
		return 0;

	if ((dev == makedev(5, 0)) && (mode & S_IFCHR)) // /dev/tty
		return 0;

	if ((dev == makedev(1, 9)) && (mode & S_IFCHR)) // /dev/urandom
		return 0;

	if ((dev == makedev(1, 5)) && (mode & S_IFCHR)) // /dev/zero
		return 0;

	return -EPERM;
}

static int seccomp_notify_mknod_set_response(int fd_mem, struct seccomp_notify_proxy_msg *msg,
					     char *buf, size_t size,
					     mode_t *mode, dev_t *dev,
					     pid_t *pid)
{
	struct seccomp_notif *req = &msg->req;
	struct seccomp_notif_resp *resp = &msg->resp;
	int ret;
	ssize_t bytes;

	resp->id = req->id;
	resp->flags = req->flags;
	resp->val = 0;

	if (req->data.nr != __NR_mknod) {
		resp->error = -ENOSYS;
		return -1;
	}

	resp->error = device_allowed(req->data.args[2], req->data.args[1]);
	if (resp->error)
		return -1;

	bytes = pread(fd_mem, buf, size, req->data.args[0]);
	if (bytes < 0)
		return -1;

	*mode = req->data.args[1];
	*dev = req->data.args[2];
	*pid = req->pid;

	return 0;
}

static void seccomp_notify_mknod_update_response(struct seccomp_notify_proxy_msg *msg,
						 int new_neg_errno)
{
	msg->resp.error = new_neg_errno;
}
#else
static int seccomp_notify_mknod_set_response(int fd_mem, struct seccomp_notify_proxy_msg *msg,
					     char *buf, size_t size,
					     mode_t *mode, dev_t *dev,
					     pid_t *pid)
{
	errno = ENOSYS;
	return -1;
}

static void seccomp_notify_mknod_update_response(struct seccomp_notify_proxy_msg *msg,
						 int new_neg_errno)
{
}
#endif // SECCOMP_RET_USER_NOTIF
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"

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
const SECCOMP_NOTIFY_POLICY = `mknod notify`

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

	if !c.IsPrivileged() && !c.DaemonState().OS.RunningInUserNS && lxcSupportSeccompNotify(c.DaemonState()) {
		policy += SECCOMP_NOTIFY_POLICY
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

type SeccompServer struct {
	d    *Daemon
	path string
	l    net.Listener
}

func NewSeccompServer(d *Daemon, path string) (*SeccompServer, error) {
	// Cleanup existing sockets
	if shared.PathExists(path) {
		err := os.Remove(path)
		if err != nil {
			return nil, err
		}
	}

	// Bind new socket
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}

	// Restrict access
	err = os.Chmod(path, 0700)
	if err != nil {
		return nil, err
	}

	// Start the server
	s := SeccompServer{
		d:    d,
		path: path,
		l:    l,
	}

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}

			go func() {
				ucred, err := getCred(c.(*net.UnixConn))
				if err != nil {
					logger.Errorf("Unable to get ucred from seccomp socket client: %v", err)
					return
				}

				logger.Debugf("Connected to seccomp socket: pid=%v", ucred.pid)

				unixFile, err := c.(*net.UnixConn).File()
				if err != nil {
					return
				}

				for {
					buf := make([]byte, C.SECCOMP_PROXY_MSG_SIZE)
					fdMem, err := netutils.AbstractUnixReceiveFdData(int(unixFile.Fd()), buf)
					if err != nil || err == io.EOF {
						logger.Debugf("Disconnected from seccomp socket after receive: pid=%v", ucred.pid)
						c.Close()
						return
					}

					go s.Handler(c, ucred, buf, fdMem)
				}
			}()
		}
	}()

	return &s, nil
}

func (s *SeccompServer) Handler(c net.Conn, ucred *ucred, buf []byte, fdMem int) error {
	logger.Debugf("Handling seccomp notification from: %v", ucred.pid)

	defer unix.Close(fdMem)
	var msg C.struct_seccomp_notify_proxy_msg
	C.memcpy(unsafe.Pointer(&msg), unsafe.Pointer(&buf[0]), C.SECCOMP_PROXY_MSG_SIZE)

	var cMode C.mode_t
	var cDev C.dev_t
	var cPid C.pid_t
	cPathBuf := [unix.PathMax]C.char{}
	ret := C.seccomp_notify_mknod_set_response(C.int(fdMem), &msg,
		&cPathBuf[0],
		unix.PathMax, &cMode,
		&cDev, &cPid)
	if ret == 0 {
		errnoMsg, err := shared.RunCommand(util.GetExecPath(),
			"forkmknod",
			fmt.Sprintf("%d", cPid),
			C.GoString(&cPathBuf[0]),
			fmt.Sprintf("%d", cMode),
			fmt.Sprintf("%d", cDev))
		if err != nil {
			cErrno := C.int(-C.EPERM)
			goErrno, err2 := strconv.Atoi(errnoMsg)
			if err2 == nil {
				cErrno = -C.int(goErrno)
			}

			C.seccomp_notify_mknod_update_response(&msg, cErrno)
			logger.Errorf("Failed to create device node: %s", err)
		}
	}

	C.memcpy(unsafe.Pointer(&buf[0]), unsafe.Pointer(&msg), C.SECCOMP_PROXY_MSG_SIZE)

	_, err := c.Write(buf)
	if err != nil {
		logger.Debugf("Disconnected from seccomp socket after write: pid=%v", ucred.pid)
		return err
	}

	logger.Debugf("Handled seccomp notification from: %v", ucred.pid)
	return nil
}

func (s *SeccompServer) Stop() error {
	os.Remove(s.path)
	return s.l.Close()
}
