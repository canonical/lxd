(instances-noefi)=
# How to boot a VM without UEFI

You can optionally configure LXD virtual machines to boot using the lighter-weight U-Boot firmware
instead of LXD's default {abbr}`OVMF (Open Virtual Machine Firmware)`.

U-Boot has limited features compared to OVMF, but is a little faster and simpler. It uses a `GPL-v2`
license instead of BSD.

Some common use cases include:

- Situations where minimal boot time is very important.
- Booting Operating Systems which don't have an `EFI` stub, for example a build of Linux which does
  not have `CONFIG_EFI_STUB` enabled.
- Booting an Operating System which is not found by the normal `EFI` Boot Manager mechanism, e.g.
  because it is listed in an `extlinux.conf` file
- Where manual tweaks are desired before the Operating System boots, such as device tree updates
  and kernel command-line changes

```{admonition} Not for production use
:class: warning
This option has not been tested on real-world workloads and is not yet ready for production use. Use in testing or development environments only.

While U-Boot starts without using `EFI` protocols, many Operating System are packaged in such a
way that `EFI` protocols are necessary for booting. U-Boot's `EFI_LOADER` feature supports this,
meaning that `EFI` is still used in the boot process. Options exist for avoiding this, such as
installing the `u-boot-menu` package in the guest.

U-Boot's environment is not currently supported in the `u-boot-qemu` package, so any changes to
U-Boot's environment variables do not persist across reboots.
```

(instances-noefi-requirements)=
## Requirement

- The `u-boot-qemu` package must be installed on the host.

(instances-noefi-configure)=
## Configure booting without `EFI`

On the LXD host, run:

```bash
lxc config set <vm-name> boot.noefi=true
```

The allowed values are:

- `true`: This LXD VM boots with U-Boot instead of OVMF.
- `false`: Default if unset. This LXD VM boots using OVMF.

To set this key on an existing VM, see: {ref}`instances-configure-options`.

(instances-noefi-view)=
## View startup process

To view information about the U-Boot startup process on a VM with `boot.noefi` enabled, start it with the `--console` flag:

```bash
lxc start <vm-name> --console
```

The output should confirm that U-Boot is handling startup as intended.

The example below shows a VM booting with U-Boot, then locating Ubuntu on a `virtio-scsi` device and starting it.

```{terminal}
:input: lxc start test-vm --console
    To detach from the console, press: <ctrl>+a q

    U-Boot Concept SPL 2025.01-rc3-01944-ga54ca38dc2ed-dirty (Jul 16 2025 - 13:02:30 -0600)
    Trying to boot from SPI


    U-Boot Concept 2025.01-rc3-01944-ga54ca38dc2ed (Jul 15 2025 - 16:33:52 -0600)

    CPU:   AMD Ryzen 9 7950X 16-Core Processor
    DRAM:  1 GiB
    Core:  19 devices, 12 uclasses, devicetree: separate
    Loading Environment from nowhere... OK
    Model: QEMU x86 (Q35)
    Net:         eth_initialize() No ethernet found.
    Hit any key to stop autoboot:  0
    Scanning for bootflows in all bootdevs
    Seq  Method       State   Uclass    Part  Name                      Filename
    ---  -----------  ------  --------  ----  ------------------------  ----------------
    Scanning global bootmeth 'efi_mgr':
         efi_var_to_file() Cannot persist EFI variables without system partition
        efi_rng_register() Missing RNG device for EFI_RNG_PROTOCOL
    Hunting with: virtio
    Hunting with: fs
    Scanning bootdev 'virtio-fs#8.bootdev':
    Hunting with: nvme
    Hunting with: qfw
    Hunting with: scsi
    scanning bus for devices...
      Device 0: (0:1) Vendor: QEMU Prod.: QEMU HARDDISK Rev: 2.5+
                Type: Hard Disk
                Capacity: 10240.0 MB = 10.0 GB (20971520 x 512)
    Scanning bootdev 'qfw_pio.bootdev':
    Scanning bootdev 'virtio-scsi#6.id0lun1.bootdev':
      0  efi          ready   scsi         f  virtio-scsi#6.id0lun1.boo /EFI/BOOT/BOOTX64.EFI
    ** Booting bootflow 'virtio-scsi#6.id0lun1.bootdev.part_f' with efi
           efi_run_image() Booting /\EFI\BOOT\BOOTX64.EFI
    EFI stub: Loaded initrd from LINUX_EFI_INITRD_MEDIA_GUID device path

    Starting kernel ...

    Timer summary in microseconds (8 records):
           Mark    Elapsed  Stage
              0          0  reset
        258,496    258,496  board_init_f
        264,987      6,491  board_init_r
        331,435     66,448  eth_common_init
        334,052      2,617  main_loop
        904,841    570,789  start_kernel

    Accumulated time:
                        46  dm_f
                        41  dm_r
    [    0.000000] Linux version 6.8.0-63-generic (buildd@lcy02-amd64-047) (x86_64-linux-gnu-gcc-13 (Ubuntu 13.3.0-6ubuntu2~24.04) 13.3.0, GNU ld (GNU Binutils for Ubuntu) 2.42) #66-Ubuntu SMP PREEMPT_DYNAMIC Fri Jun 13 20:25:30 UTC 2025 (Ubuntu 6.8.0-63.66-generic 6.8.12)
```
