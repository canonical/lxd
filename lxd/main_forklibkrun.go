package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxd/instance/drivers"
	"github.com/canonical/lxd/lxd/instance/drivers/libkrun"
	"github.com/canonical/lxd/shared"
)

type cmdForklibkrun struct {
	global *cmdGlobal

	flagConfig string

	// flagProject and flagInstance are not read back into Go logic; they are passed so
	// that the connecting MicroVM can be identified from this process's own
	// /proc/<pid>/cmdline (see authenticateMicroVMAgentCert in api_vsock.go).
	flagProject  string
	flagInstance string
}

func (c *cmdForklibkrun) command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forklibkrun"
	cmd.Short = "Run a MicroVM using libkrun"
	cmd.Long = `Description:
  Run a MicroVM using libkrun.

  This internal command configures and boots a libkrun MicroVM from a configuration
  file. libkrun's krun_start_enter() takes over the calling process and does not
  return, so this command is spawned as a dedicated child process by the LXD daemon.
`
	cmd.RunE = c.run
	cmd.Hidden = true

	cmd.Flags().StringVar(&c.flagConfig, "config", "", "Path to the MicroVM configuration file")
	cmd.Flags().StringVar(&c.flagProject, "project", "", "Instance project (used for MicroVM identification via /proc/<pid>/cmdline)")
	cmd.Flags().StringVar(&c.flagInstance, "instance", "", "Instance name (used for MicroVM identification via /proc/<pid>/cmdline)")

	return cmd
}

// kernelFormat maps a format name to the libkrun kernel format constant. When set to "auto"
// the format is detected from the kernel image's magic bytes.
func kernelFormat(fmtName string, kernelPath string) (libkrun.KernelFormat, error) {
	switch fmtName {
	case "auto", "":
		return detectKernelFormat(kernelPath)
	case "raw":
		return libkrun.KernelFormatRaw, nil
	case "elf":
		return libkrun.KernelFormatELF, nil
	case "pe_gz":
		return libkrun.KernelFormatPEGZ, nil
	case "image_gz":
		return libkrun.KernelFormatImageGZ, nil
	case "image_bz2":
		return libkrun.KernelFormatImageBZ2, nil
	case "image_zstd":
		return libkrun.KernelFormatImageZstd, nil
	default:
		return 0, fmt.Errorf("Unsupported kernel format %q", fmtName)
	}
}

// parseHWAddr parses a hardware MAC address into a 6-byte array.
func parseHWAddr(hwaddr string) ([6]byte, error) {
	hw, err := net.ParseMAC(hwaddr)
	if err != nil {
		return [6]byte{}, fmt.Errorf("Invalid hardware address %q: %w", hwaddr, err)
	}

	if len(hw) != 6 {
		return [6]byte{}, fmt.Errorf("Unsupported hardware address %q, expected 6 bytes", hwaddr)
	}

	var mac [6]byte
	copy(mac[:], hw)

	return mac, nil
}

// guestHaltedMessage is what the guest kernel prints on its console once it has powered off.
// libkrun does not report a guest power off, so the helper watches the console for it.
const guestHaltedMessage = "reboot: Power off not available: System halted instead"

// scanConsoleForHalt scans the guest console output for guestHaltedMessage with a sliding window.
// Pass the returned tail to the next call, so a message split over two chunks is still found.
func scanConsoleForHalt(tail []byte, chunk []byte) (found bool, newTail []byte) {
	tail = append(tail, chunk...)
	if bytes.Contains(tail, []byte(guestHaltedMessage)) {
		return true, tail
	}

	// Keep only what could still be the start of the message.
	keep := len(guestHaltedMessage) - 1
	if len(tail) > keep {
		tail = append(tail[:0], tail[len(tail)-keep:]...)
	}

	return false, tail
}

// detectKernelFormat inspects the leading magic bytes of a kernel image to determine its format.
func detectKernelFormat(path string) (libkrun.KernelFormat, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("Failed opening kernel %q for format detection: %w", path, err)
	}

	defer func() { _ = f.Close() }()

	hdr := make([]byte, 4)
	_, err = io.ReadFull(f, hdr)
	if err != nil {
		return 0, fmt.Errorf("Failed reading kernel %q for format detection: %w", path, err)
	}

	switch {
	case bytes.Equal(hdr, []byte{0x7f, 'E', 'L', 'F'}):
		// Uncompressed ELF vmlinux.
		return libkrun.KernelFormatELF, nil
	case bytes.Equal(hdr[:2], []byte{'M', 'Z'}):
		// x86 bzImage with EFI PE header (typically gzip-compressed payload).
		return libkrun.KernelFormatPEGZ, nil
	case bytes.Equal(hdr[:2], []byte{0x1f, 0x8b}):
		// gzip-compressed image.
		return libkrun.KernelFormatImageGZ, nil
	case bytes.Equal(hdr, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		// zstd-compressed image.
		return libkrun.KernelFormatImageZstd, nil
	case bytes.Equal(hdr[:3], []byte{'B', 'Z', 'h'}):
		// bzip2-compressed image.
		return libkrun.KernelFormatImageBZ2, nil
	default:
		return 0, fmt.Errorf("Could not detect kernel format for %q (magic %#x); set \"kernel.format\" in the MicroVM config", path, hdr)
	}
}

func (c *cmdForklibkrun) run(_ *cobra.Command, _ []string) error {
	// Print a backtrace to the log file if libkrun panics.
	err := os.Setenv("RUST_BACKTRACE", "1")
	if err != nil {
		return fmt.Errorf("Failed setting RUST_BACKTRACE: %w", err)
	}

	// Only root should run this.
	if os.Geteuid() != 0 {
		return errors.New("This must be run as root")
	}

	if c.flagConfig == "" {
		return errors.New("Missing required --config argument")
	}

	cfg, err := drivers.ReadMicroVMConfig(c.flagConfig)
	if err != nil {
		return err
	}

	kFmt, err := kernelFormat(cfg.Kernel.Format, cfg.Kernel.Path)
	if err != nil {
		return err
	}

	// Load the libkrun library that the daemon found and checked. The loader reads
	// LIBKRUN_PATH on the first libkrun call, which is the context creation below.
	// When the daemon found none, the path is empty and the loader searches the system
	// library path instead.
	if cfg.LibkrunPath != "" {
		err = os.Setenv("LIBKRUN_PATH", cfg.LibkrunPath)
		if err != nil {
			return fmt.Errorf("Failed setting LIBKRUN_PATH: %w", err)
		}
	}

	// Create the libkrun configuration context.
	ctx, err := libkrun.CreateContext()
	if err != nil {
		return fmt.Errorf("Failed creating libkrun context: %w", err)
	}

	defer func() { _ = ctx.Close() }()

	// Configure vCPUs and memory.
	err = ctx.SetVMConfig(cfg.CPUs, cfg.MemoryMiB)
	if err != nil {
		return fmt.Errorf("Failed configuring vCPUs/RAM: %w", err)
	}

	// The guest console (console=hvc0) is the default virtio-console. It is configured first,
	// as in libkrun's own external_kernel example. With the pinned libkrun revision the order in
	// which the devices are added was not found to matter.

	// Create a PTY pair for the console. The guest console is wired to the PTY slave, while
	// the PTY master is bridged to a UNIX socket that the LXD daemon connects to on demand.
	ptx, pty, err := shared.OpenPty(-1, -1)
	if err != nil {
		return fmt.Errorf("Failed opening console PTY: %w", err)
	}

	// Keep the PTY slave referenced until the helper exits. libkrun is only given the descriptor
	// number and duplicates it when the VM starts. Without a reference, the garbage collector can
	// close the file before that, which leaves the guest without a console.
	defer func() { _ = pty.Close() }()

	// Bridge the PTY master to the console socket. This goroutine is started before StartEnter
	// so it keeps running while libkrun runs the VM on the main thread.
	_ = os.Remove(cfg.Console)
	listener, err := net.Listen("unix", cfg.Console)
	if err != nil {
		return fmt.Errorf("Failed creating console socket %q: %w", cfg.Console, err)
	}

	var consoleConnMu sync.Mutex
	var consoleConn net.Conn

	// Always drain the PTY master so guest writes never block when no client is attached.
	// The go routine below also filters the console output for poweroff messages.
	go func() {
		buf := make([]byte, 32768)
		var consoleTail []byte
		var halted bool

		for {
			n, err := ptx.Read(buf)
			if err != nil {
				return
			}

			halted, consoleTail = scanConsoleForHalt(consoleTail, buf[:n])
			if halted {
				fmt.Fprintln(os.Stderr, "Guest reported halted state, stopping forklibkrun")

				// The helper must exit from this goroutine; status 1 means stop rather than reboot.
				os.Exit(1) //nolint:revive
			}

			consoleConnMu.Lock()
			conn := consoleConn
			consoleConnMu.Unlock()

			if conn != nil {
				_, err = conn.Write(buf[:n])
				if err != nil {
					consoleConnMu.Lock()
					if consoleConn == conn {
						_ = consoleConn.Close()
						consoleConn = nil
					}

					consoleConnMu.Unlock()
				}
			}
		}
	}()

	// This go routine bridges libkrun console socket with the ptx (correspondingly to pty which is inside the vm).
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			consoleConnMu.Lock()
			if consoleConn != nil {
				_ = consoleConn.Close()
			}

			consoleConn = conn
			consoleConnMu.Unlock()

			go func(conn net.Conn) {
				_, _ = io.Copy(ptx, conn)

				consoleConnMu.Lock()
				if consoleConn == conn {
					_ = consoleConn.Close()
					consoleConn = nil
				}

				consoleConnMu.Unlock()
			}(conn)
		}
	}()

	// Wire the guest console to the PTY slave.
	err = ctx.AddVirtioConsoleDefault(int(pty.Fd()), int(pty.Fd()), int(pty.Fd()))
	if err != nil {
		return fmt.Errorf("Failed configuring console: %w", err)
	}

	// Add a separate multi-port virtio-console for named service ports used by
	// lxd-agent activation plumbing.
	activationConsoleID, err := ctx.AddVirtioConsoleMultiport()
	if err != nil {
		return fmt.Errorf("Failed adding activation multi-port console: %w", err)
	}

	// lxd-agent writes its status to this port; without an output libkrun never drains it and
	// the agent blocks on write, stalling guest shutdown until systemd kills it.
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("Failed opening %q: %w", os.DevNull, err)
	}

	defer func() { _ = devNull.Close() }()

	err = ctx.AddConsolePortInout(activationConsoleID, "com.canonical.lxd", -1, int(devNull.Fd()))
	if err != nil {
		return fmt.Errorf("Failed configuring lxd-agent activation port: %w", err)
	}

	// Configure the kernel and command line. MicroVMs boot without an initrd.
	err = ctx.SetKernel(cfg.Kernel.Path, kFmt, "", cfg.Kernel.Cmdline)
	if err != nil {
		return fmt.Errorf("Failed configuring kernel: %w", err)
	}

	// Add the root disk as a virtio-blk device (appears as /dev/vda in the guest).
	err = ctx.AddDisk("root", cfg.RootDisk, false)
	if err != nil {
		return fmt.Errorf("Failed configuring root disk: %w", err)
	}

	// Expose the config drive over virtio-fs with the "config" tag expected by lxd-agent.
	err = ctx.AddVirtioFS3("config", cfg.ConfigDrive, 0, true)
	if err != nil {
		return fmt.Errorf("Failed configuring config drive virtio-fs: %w", err)
	}

	// Wire vsock for lxd-agent connectivity: AddVsockPort2 bridges a host unix socket to the
	// guest agent port (LXD->agent), AddVsockPort forwards guest dials on LXDPort to the host (agent->LXD).
	if cfg.Vsock != nil {
		err = ctx.AddVsock(0)
		if err != nil {
			return fmt.Errorf("Failed adding vsock device: %w", err)
		}

		// HTTPSDefaultPort: 8443 - AgentSocket: libkrun.agent.sock
		err = ctx.AddVsockPort2(shared.HTTPSDefaultPort, cfg.Vsock.AgentSocket, true)
		if err != nil {
			return fmt.Errorf("Failed adding vsock agent port: %w", err)
		}

		// LXDPort: libkrunAgentToLXDPort (8444) - LXDSocket: vsock-unix.socket
		err = ctx.AddVsockPort(cfg.Vsock.LXDPort, cfg.Vsock.LXDSocket)
		if err != nil {
			return fmt.Errorf("Failed adding vsock LXD port: %w", err)
		}
	}

	// Add network interfaces backed by host TAP devices. libkrun opens the named TAP device
	// itself and attaches it as a virtio-net device. Interfaces appear in the guest as eth0,
	// eth1, ... in the order they are added.
	for _, nic := range cfg.NICs {
		mac, err := parseHWAddr(nic.HWAddr)
		if err != nil {
			return err
		}

		err = ctx.AddNetTap(nic.Tap, mac, libkrun.CompatNetFeatures, 0)
		if err != nil {
			return fmt.Errorf("Failed configuring network interface %q: %w", nic.Tap, err)
		}
	}

	// StartEnter normally does not return: on success libkrun runs the VM and terminates this
	// process with the workload's exit code.
	err = ctx.StartEnter()
	if err != nil {
		return fmt.Errorf("Failed starting libkrun MicroVM: %w", err)
	}

	return nil
}
