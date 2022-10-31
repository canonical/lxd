(devices)=
# Devices

Devices are attached to an instance.
They include, for example, network interfaces, mount points, USB and GPU devices.
These devices can have instance device options, depending on the type of the instance device.

## Standard devices

LXD will always provide the instance with the basic devices which are required
for a standard POSIX system to work. These aren't visible in instance or
profile configuration and may not be overridden.

Those include:

- `/dev/null` (character device)
- `/dev/zero` (character device)
- `/dev/full` (character device)
- `/dev/console` (character device)
- `/dev/tty` (character device)
- `/dev/random` (character device)
- `/dev/urandom` (character device)
- `/dev/net/tun` (character device)
- `/dev/fuse` (character device)
- `lo` (network interface)

Anything else has to be defined in the instance configuration or in one of its
profiles. The default profile will typically contain a network interface to
become `eth0` in the instance.

## How to add devices

To add extra devices to an instance, device entries can be added directly to an
instance, or to a profile.

Devices may be added or removed while the instance is running.

Every device entry is identified by a unique name. If the same name is used in
a subsequent profile or in the instance's own configuration, the whole entry
is overridden by the new definition.

Device names are limited to a maximum of 64 characters.

Device entries are added to an instance through:

```bash
lxc config device add <instance> <name> <type> [key=value]...
```

or to a profile with:

```bash
lxc profile device add <profile> <name> <type> [key=value]...
```

(device-types)=
## Device types

LXD supports the following device types:

| ID (database) | Name                                   | Condition | Description                     |
|:--------------|:---------------------------------------|:----------|:--------------------------------|
| 0             | [`none`](devices-none)                 | -         | Inheritance blocker             |
| 1             | [`nic`](devices-nic)                   | -         | Network interface               |
| 2             | [`disk`](devices-disk)                 | -         | Mount point inside the instance |
| 3             | [`unix-char`](devices-unix-char)       | container | Unix character device           |
| 4             | [`unix-block`](devices-unix-block)     | container | Unix block device               |
| 5             | [`usb`](devices-usb)                   | -         | USB device                      |
| 6             | [`gpu`](devices-gpu)                   | -         | GPU device                      |
| 7             | [`infiniband`](devices-infiniband)     | container | InfiniBand device               |
| 8             | [`proxy`](devices-proxy)               | container | Proxy device                    |
| 9             | [`unix-hotplug`](devices-unix-hotplug) | container | Unix hotplug device             |
| 10            | [`tpm`](devices-tpm)                   | -         | TPM device                      |
| 11            | [`pci`](devices-pci)                   | VM        | PCI device                      |

```{toctree}
:maxdepth: 1
:hidden:

../reference/devices_none.md
../reference/devices_nic.md
../reference/devices_disk.md
../reference/devices_unix_char.md
../reference/devices_unix_block.md
../reference/devices_usb.md
../reference/devices_gpu.md
../reference/devices_infiniband.md
../reference/devices_proxy.md
../reference/devices_unix_hotplug.md
../reference/devices_tpm.md
../reference/devices_pci.md
```
