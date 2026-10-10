package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
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
	// /proc/<pid>/cmdline (see libkrunVsockProxyIdentity in driver_microvm.go).
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

	// Configure the virtio-console before any other virtio device. libkrun assigns virtio-mmio
	// device slots in the order devices are added, and the guest's console=hvc0 relies on the
	// console being the first virtio device. Adding it after the disk and network devices leaves
	// the guest console broken partway through boot (early kernel output appears, then stops as
	// the other virtio devices come up). This matches the ordering used by libkrun's own
	// external_kernel example.

	// Create a PTY pair for the console. The guest console is wired to the PTY slave, while
	// the PTY master is bridged to a UNIX socket that the LXD daemon connects to on demand.
	ptx, pty, err := shared.OpenPty(-1, -1)
	if err != nil {
		return fmt.Errorf("Failed opening console PTY: %w", err)
	}

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
	go func() {
		const haltedLine = "reboot: Power off not available: System halted instead"

		buf := make([]byte, 32768)
		lineBuf := make([]byte, 0, 4096)

		checkLine := func(line string) {
			if strings.Contains(line, haltedLine) {
				fmt.Fprintln(os.Stderr, "Guest reported halted state, stopping forklibkrun")
				os.Exit(1) //nolint:revive // The helper must exit from this goroutine; status 1 means stop rather than reboot.
			}
		}

		for {
			n, err := ptx.Read(buf)
			if err != nil {
				return
			}

			lineBuf = append(lineBuf, buf[:n]...)
			for {
				lineEnd := bytes.IndexByte(lineBuf, '\n')
				if lineEnd == -1 {
					break
				}

				line := strings.TrimRight(string(lineBuf[:lineEnd]), "\r")
				checkLine(line)

				lineBuf = lineBuf[lineEnd+1:]
			}

			if len(lineBuf) > 0 {
				checkLine(string(lineBuf))
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

		err = ctx.AddVsockPort2(shared.HTTPSDefaultPort, cfg.Vsock.AgentSocket, true)
		if err != nil {
			return fmt.Errorf("Failed adding vsock agent port: %w", err)
		}

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
