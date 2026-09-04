package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxd/instance/drivers/libkrun"
	"github.com/canonical/lxd/shared"
)

type cmdForklibkrun struct {
	global *cmdGlobal

	flagCPUs        string
	flagMemory      string
	flagKernel      string
	flagKernelFmt   string
	flagInitrd      string
	flagCmdline     string
	flagRootDisk    string
	flagConfigDrive string
	flagConsolePath string
	flagNICs        []string
	flagLXDPath     string
	flagProject     string
	flagInstance    string

	// Vsock flags for lxd-agent connectivity.
	// flagVsockAgentSocket is the unix socket path libkrun creates so LXD can
	// connect to the guest's vsock agent listener (LXD→agent direction).
	// flagVsockLxdPort is the vsock port number that the guest agent dials to
	// reach the LXD vsock server (agent→LXD direction); libkrun intercepts this
	// and forwards to flagVsockLxdSocket on the host.
	flagVsockAgentSocket string
	flagVsockLxdPort     uint
	flagVsockLxdSocket   string
}

func (c *cmdForklibkrun) command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forklibkrun"
	cmd.Short = "Run a MicroVM using libkrun"
	cmd.Long = `Description:
  Run a MicroVM using libkrun.

  This internal command configures and boots a libkrun MicroVM. libkrun's
  krun_start_enter() takes over the calling process and does not return, so
  this command is spawned as a dedicated child process by the LXD daemon.
`
	cmd.RunE = c.run
	cmd.Hidden = true

	cmd.Flags().StringVar(&c.flagCPUs, "cpus", "1", "Number of vCPUs")
	cmd.Flags().StringVar(&c.flagMemory, "memory", "", "Amount of RAM in MiB")
	cmd.Flags().StringVar(&c.flagKernel, "kernel", "", "Path to the kernel image")
	cmd.Flags().StringVar(&c.flagKernelFmt, "kernel-format", "auto", "Kernel image format (auto, raw, elf, pe_gz, image_gz, image_bz2, image_zstd)")
	cmd.Flags().StringVar(&c.flagInitrd, "initrd", "", "Path to the initrd image")
	cmd.Flags().StringVar(&c.flagCmdline, "cmdline", "", "Kernel command line")
	cmd.Flags().StringVar(&c.flagRootDisk, "root-disk", "", "Path to the root disk image")
	cmd.Flags().StringVar(&c.flagConfigDrive, "config-drive", "", "Path to the config drive directory to expose via virtio-fs")
	cmd.Flags().StringVar(&c.flagConsolePath, "console", "", "Path to the console socket to expose")

	cmd.Flags().StringArrayVar(&c.flagNICs, "net", nil, "Network interface as \"tap_name,hwaddr\" (repeatable)")
	cmd.Flags().StringVar(&c.flagLXDPath, "lxd-path", "", "Path to LXD state directory for stop callback")
	cmd.Flags().StringVar(&c.flagProject, "project", "", "Instance project for stop callback")
	cmd.Flags().StringVar(&c.flagInstance, "instance", "", "Instance name for stop callback")

	cmd.Flags().StringVar(&c.flagVsockAgentSocket, "vsock-agent-socket", "", "Unix socket path for LXD→agent vsock bridge (optional)")
	cmd.Flags().UintVar(&c.flagVsockLxdPort, "vsock-lxd-port", 0, "Vsock port the guest agent dials to reach LXD (optional)")
	cmd.Flags().StringVar(&c.flagVsockLxdSocket, "vsock-lxd-socket", "", "Unix socket path for agent→LXD vsock bridge (optional)")

	return cmd
}

// kernelFormat maps a format name to the libkrun kernel format constant. When set to "auto"
// the format is detected from the kernel image's magic bytes.
func (c *cmdForklibkrun) kernelFormat() (libkrun.KernelFormat, error) {
	switch c.flagKernelFmt {
	case "auto":
		return detectKernelFormat(c.flagKernel)
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
		return 0, fmt.Errorf("Unsupported kernel format %q", c.flagKernelFmt)
	}
}

// parseNICArg parses a "tap_name,hwaddr" NIC argument into its TAP device name and MAC address.
func parseNICArg(arg string) (string, [6]byte, error) {
	tapName, hwaddr, ok := strings.Cut(arg, ",")
	if !ok || tapName == "" || hwaddr == "" {
		return "", [6]byte{}, fmt.Errorf("Invalid --net value %q, expected \"tap_name,hwaddr\"", arg)
	}

	hw, err := net.ParseMAC(hwaddr)
	if err != nil {
		return "", [6]byte{}, fmt.Errorf("Invalid hardware address %q: %w", hwaddr, err)
	}

	if len(hw) != 6 {
		return "", [6]byte{}, fmt.Errorf("Unsupported hardware address %q, expected 6 bytes", hwaddr)
	}

	var mac [6]byte
	copy(mac[:], hw)

	return tapName, mac, nil
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
		return 0, fmt.Errorf("Could not detect kernel format for %q (magic %#x); pass --kernel-format explicitly", path, hdr)
	}
}

func (c *cmdForklibkrun) run(_ *cobra.Command, _ []string) error {
	err := os.Setenv("RUST_LOG", "verbose")
	if err != nil {
		return fmt.Errorf("Failed setting RUST_LOG: %w", err)
	}

	err = os.Setenv("RUST_BACKTRACE", "1")
	if err != nil {
		return fmt.Errorf("Failed setting RUST_BACKTRACE: %w", err)
	}

	// Only root should run this.
	if os.Geteuid() != 0 {
		return errors.New("This must be run as root")
	}

	// Validate required arguments.
	if c.flagKernel == "" {
		return errors.New("Missing required --kernel argument")
	}

	if c.flagRootDisk == "" {
		return errors.New("Missing required --root-disk argument")
	}

	if c.flagConfigDrive == "" {
		return errors.New("Missing required --config-drive argument")
	}

	if c.flagConsolePath == "" {
		return errors.New("Missing required --console argument")
	}

	if c.flagMemory == "" {
		return errors.New("Missing required --memory argument")
	}

	cpus, err := strconv.ParseUint(c.flagCPUs, 10, 8)
	if err != nil {
		return fmt.Errorf("Invalid --cpus value: %w", err)
	}

	memMiB, err := strconv.ParseUint(c.flagMemory, 10, 32)
	if err != nil {
		return fmt.Errorf("Invalid --memory value: %w", err)
	}

	kernelFormat, err := c.kernelFormat()
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
	err = ctx.SetVMConfig(uint8(cpus), uint32(memMiB))
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
	_ = os.Remove(c.flagConsolePath)
	listener, err := net.Listen("unix", c.flagConsolePath)
	if err != nil {
		return fmt.Errorf("Failed creating console socket %q: %w", c.flagConsolePath, err)
	}

	var consoleConnMu sync.Mutex
	var consoleConn net.Conn

	// Always drain the PTY master so guest writes never block when no client is attached.
	go func() {
		const haltedLine = "reboot: Power off not available: System halted instead"

		buf := make([]byte, 32768)
		lineBuf := make([]byte, 0, 4096)
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
				if strings.Contains(line, haltedLine) {
					fmt.Fprintln(os.Stderr, "Guest reported halted state, stopping forklibkrun")
					os.Exit(1) //nolint:revive // The helper must exit from this goroutine; status 1 means stop rather than reboot.
				}

				lineBuf = lineBuf[lineEnd+1:]
			}

			if strings.Contains(string(lineBuf), haltedLine) {
				fmt.Fprintln(os.Stderr, "Guest reported halted state, stopping forklibkrun")
				os.Exit(1) //nolint:revive // The helper must exit from this goroutine; status 1 means stop rather than reboot.
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

	err = ctx.AddConsolePortInout(activationConsoleID, "com.canonical.lxd", -1, -1)
	if err != nil {
		return fmt.Errorf("Failed configuring lxd-agent activation port: %w", err)
	}

	// Configure the kernel, initrd and command line.
	err = ctx.SetKernel(c.flagKernel, kernelFormat, c.flagInitrd, c.flagCmdline)
	if err != nil {
		return fmt.Errorf("Failed configuring kernel: %w", err)
	}

	// Add the root disk as a virtio-blk device (appears as /dev/vda in the guest).
	err = ctx.AddDisk("root", c.flagRootDisk, false)
	if err != nil {
		return fmt.Errorf("Failed configuring root disk: %w", err)
	}

	// Expose the config drive over virtio-fs with the "config" tag expected by lxd-agent.
	err = ctx.AddVirtioFS3("config", c.flagConfigDrive, 0, true)
	if err != nil {
		return fmt.Errorf("Failed configuring config drive virtio-fs: %w", err)
	}

	// Wire vsock for lxd-agent connectivity when the caller has provided socket paths.
	// AddVsock adds a virtio-vsock device (no TSI/transparent socket impersonation).
	// AddVsockPort2 creates a unix socket that libkrun listens on; when the LXD daemon
	// connects to it the traffic is bridged to the guest vsock port (LXD→agent).
	// AddVsockPort intercepts guest vsock connections on lxdPort and forwards them to
	// a unix socket on the host where the LXD vsock proxy listens (agent→LXD).
	if c.flagVsockAgentSocket != "" && c.flagVsockLxdPort != 0 && c.flagVsockLxdSocket != "" {
		err = ctx.AddVsock(0)
		if err != nil {
			return fmt.Errorf("Failed adding vsock device: %w", err)
		}

		err = ctx.AddVsockPort2(shared.HTTPSDefaultPort, c.flagVsockAgentSocket, true)
		if err != nil {
			return fmt.Errorf("Failed adding vsock agent port: %w", err)
		}

		err = ctx.AddVsockPort(uint32(c.flagVsockLxdPort), c.flagVsockLxdSocket)
		if err != nil {
			return fmt.Errorf("Failed adding vsock LXD port: %w", err)
		}
	}

	// Add network interfaces backed by host TAP devices. libkrun opens the named TAP device
	// itself and attaches it as a virtio-net device. Interfaces appear in the guest as eth0,
	// eth1, ... in the order they are added.
	for _, nicArg := range c.flagNICs {
		tapName, mac, err := parseNICArg(nicArg)
		if err != nil {
			return err
		}

		err = ctx.AddNetTap(tapName, mac, libkrun.CompatNetFeatures, 0)
		if err != nil {
			return fmt.Errorf("Failed configuring network interface %q: %w", tapName, err)
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
