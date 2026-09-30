(architectures)=
# Architectures

LXD can run on just about any architecture that is supported by the Linux kernel and by Go.

Some entities in LXD are tied to an architecture, for example, the instances, instance snapshots and images.

The following table lists all supported architectures including their unique identifier and the name used to refer to them.
The architecture names are typically aligned with the Linux kernel architecture names.

ID    | Kernel name   | Description                     | Personalities
:---  | :---          | :----                           | :------------
1     | `i686`        | 32bit Intel x86                 |
2     | `x86_64`      | 64bit Intel x86                 | `x86`
3     | `armv7l`      | 32bit ARMv7 little-endian       |
4     | `aarch64`     | 64bit ARMv8 little-endian       | `armv7l` (optional)
5     | `ppc`         | 32bit PowerPC big-endian        |
6     | `ppc64`       | 64bit PowerPC big-endian        | `powerpc`
7     | `ppc64le`     | 64bit PowerPC little-endian     |
8     | `s390x`       | 64bit ESA/390 big-endian        |
9     | `mips`        | 32bit MIPS                      |
10    | `mips64`      | 64bit MIPS                      | `mips`
11    | `riscv32`     | 32bit RISC-V little-endian      |
12    | `riscv64`     | 64bit RISC-V little-endian      |
13    | `armv6l`      | 32bit ARMv6 little-endian       |
14    | `armv8l`      | 32bit ARMv8 little-endian       |
15    | `loongarch64` | 64bit LoongArch                 |

```{note}
LXD cares only about the kernel architecture, not the particular userspace flavor as determined by the toolchain.

That means that LXD considers ARMv7 hard-float to be the same as ARMv7 soft-float and refers to both as `armv7l`.
If useful to the user, the exact userspace ABI may be set as an image and container property, allowing easy query.
```

## Virtual machine support

LXD only supports running virtual machines on the following host architectures:

- `x86_64`
- `aarch64`
- `ppc64le`
- `riscv64`
- `s390x`

The virtual machine guest architecture can usually be the 32bit personality of the host architecture,
so long as the virtual machine firmware is capable of booting it.

(architectures-vm-emulation)=
### Emulated architectures

When LXD runs from the snap on an `x86_64` host, it can also run `riscv64` and `armv7l` virtual machines through QEMU's software emulation (TCG).
Those architectures are listed in the `vm_emulated_architectures` field of the server environment:

```bash
lxc query /1.0 | jq .environment.vm_emulated_architectures
```

For example, to launch emulated virtual machines from the official Ubuntu images:

```bash
lxc launch ubuntu:26.04/riscv64 v1 --vm -c boot.mode=uefi-nosecureboot
lxc launch ubuntu:26.04/armhf v2 --vm -c boot.mode=uefi-nosecureboot
```

Emulated virtual machines have the following limitations:

- They are considerably slower than virtual machines using hardware virtualization.
- Secure Boot is not available, so {config:option}`instance-boot:boot.mode` must be set to `uefi-nosecureboot`.
- The LXD agent is not available, so commands like `lxc exec` and `lxc file` do not work.
- Automatic cluster placement does not consider emulated architectures, so use `--target` to create such instances in a cluster.
- `armv7l` virtual machines boot using `U-Boot` firmware and default to `virtio-blk` for their disks.
