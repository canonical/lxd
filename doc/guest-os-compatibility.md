---
myst:
  html_meta:
    description: Compatibility matrix for guest operating systems in LXD virtual machines and containers.
---

(guest-os-compatibility)=
# Guest OS compatibility

## Virtual machines

The following operating systems (OS) were tested as virtual machine guest running on top of on LXD `5.21/stable`. Each OS was tested by doing a manual installation using the official ISO as provided by the vendor.

OS vendor | OS version         | OS support | [LXD agent](#lxd-agent) | VirtIO-SCSI | VirtIO-BLK | NVMe    | CSM (BIOS) | UEFI | Secure Boot
:---      | :---               | :---       | :---                    | :---        | :---       | :---    | :---       | :--- | :---
CentOS    | CentOS 6.10 [^1]   | EOL        | ❌ [^2]                 | ✅          | ❌ [^6]    | 🟢      | ✅         | ❌   | ❌
CentOS    | CentOS 7.9         | EOL        | ❌ [^2]                 | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
CentOS    | CentOS 8.5         | EOL        | ✅                      | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
CentOS    | CentOS 8-Stream    | EOL        | ✅                      | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
CentOS    | CentOS 9-Stream    | Supported  | ✅                      | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
Red Hat   | RHEL 7.9           | EOL        | ❌ [^2]                 | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
Red Hat   | RHEL 8.10          | Supported  | ✅                      | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
Red Hat   | RHEL 9.4           | Supported  | ✅                      | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
SUSE      | SLES 12 SP5        | Supported  | ✅                      | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
SUSE      | SLES 15 SP6        | Supported  | ✅                      | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
Ubuntu    | 14.04.6 LTS        | EOL        | ❌ [^7]                 | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
Ubuntu    | 16.04.7 LTS        | ESM        | ✅ [^8][^9]             | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
Ubuntu    | 18.04.6 LTS        | ESM        | ✅ [^9]                 | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
Ubuntu    | 20.04.6 LTS        | Supported  | ✅                      | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
Ubuntu    | 22.04.4 LTS        | Supported  | ✅                      | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
Ubuntu    | 24.04.1 LTS        | Supported  | ✅                      | ✅          | 🟢         | 🟢      | 🟢         | ✅   | ✅
Windows   | Server 2012        | Supported  | ➖                      | ✅          | 🟢         | ❌      | 🟢         | ✅   | ✅
Windows   | Server 2016        | Supported  | ➖                      | ✅          | 🟢         | 🟢 [^3] | ❌ [^5]    | ✅   | ✅
Windows   | Server 2019        | Supported  | ➖                      | ✅          | 🟢         | 🟢      | ❌ [^5]    | ✅   | ✅
Windows   | Server 2022        | Supported  | ➖                      | ✅          | 🟢         | 🟢      | ❌ [^5]    | ✅   | ✅
Windows   | 10 22H2            | Supported  | ➖                      | ✅          | 🟢         | 🟢      | ❌ [^5]    | ✅   | ✅
Windows   | 11 23H2 [^4]       | Supported  | ➖                      | ✅          | 🟢         | 🟢      | ❌         | ✅   | ✅

[^1]: No network support despite having VirtIO-NET module.
[^2]: Support for 9P or `virtiofs` not available. Note: CentOS 7 has a `kernel-plus` kernel with 9P support allowing LXD agent to work (with `selinux=0`).
[^3]: NVMe disks are visible but the installer lists all 255 namespaces slowing down the initialization.
[^4]: A virtual TPM is required.
[^5]: The OS installer stalls when booting in CSM/BIOS mode.
[^6]: The OS installer stalls when booting with VirtIO-BLK despite having VirtIO-BLK supported by the kernel.
[^7]: This Linux version does not use `systemd` which the LXD agent requires.
[^8]: Requires the HWE kernel (`4.15`) for proper `vsock` support which is required by the LXD agent.
[^9]: The `lxd-agent-installer` package is not available so `lxd-agent` has to be manually setup (see {ref}`lxd-agent-manual-install`) or through `cloud-init` (see {ref}`vm-cloud-init-config`).

Legend         | Icon
:---           | :---
recommended    | ✅
supported      | 🟢
not applicable | ➖
not supported  | ❌

## Notes

### LXD agent

The LXD agent provides the ability to execute commands inside of the virtual machine guest without relying on traditional access solution like secure shell (SSH) or Remote Desktop Protocol (RDP). This agent is only supported on Linux guests using `systemd`.
For how to manually setup the agent, see {ref}`lxd-agent-manual-install`.

### BIOS boot

```bash
lxc config set v1 security.secureboot=false
lxc config set v1 boot.mode=bios
```

### Virtual TPM

```bash
lxc config device add v1 vtpm tpm path=/dev/tpm0
```

### VirtIO-BLK or NVMe

```bash
lxc config device override v1 root io.bus=virtio-blk
# or
lxc config device override v1 root io.bus=nvme
```

### Disconnect the ISO

```bash
lxc config device remove v1 iso
```

## Containers

Unlike virtual machines, container guests rely on the host's kernel for execution. Since each Linux distribution ships with a unique set of features supported by their official kernels, the possibilities are almost endless.
As such, the following compatibility table focuses on hosts running Ubuntu LTS releases with LXD `5.21/stable` and Ubuntu releases as container guests. The main compatibility factor is the `cgroup` version required by the container and supported by the host.

Host OS   /  Guest OS             | Ubuntu 16.04 LTS | Ubuntu 18.04 LTS | Ubuntu 20.04 LTS | Ubuntu 22.04 LTS | Ubuntu 24.04 LTS | Ubuntu 24.10
:---                              | :---             | :---             | :---             | :---             | :---             | :---
Ubuntu 20.04 LTS 5.4.0      [^10] |  🟢              | 🟢               | 🟢               | 🟢               | 🟢               | ❌ [^11]
Ubuntu 20.04 LTS 5.15.0 (HWE)     |  ✅              | ✅               | ✅               | ✅               | ✅               | 🟢 [^12]
Ubuntu 22.04 LTS 5.15.0           |  🟢 [^13]        | ✅               | ✅               | ✅               | ✅               | ✅
Ubuntu 22.04 LTS 6.8.0 (HWE)      |  🟢 [^13]        | ✅               | ✅               | ✅               | ✅               | ✅
Ubuntu 24.04 LTS 6.8.0            |  🟢 [^13]        | ✅               | ✅               | ✅               | ✅               | ✅

[^10]: The 5.4.0 kernel is below the minimum required version (see {ref}`requirements`)
[^11]: Ubuntu 24.10 and later require `cgroupv2` which is not supported by Ubuntu 20.04 LTS regular kernel.
[^12]: Requires enabling `cgroupv2` support by booting with `systemd.unified_cgroup_hierarchy=1`
[^13]: Requires enabling `cgroupv1` support by booting with `systemd.unified_cgroup_hierarchy=0`

Legend         | Icon
:---           | :---
recommended    | ✅
supported      | 🟢
not applicable | ➖
not supported  | ❌
