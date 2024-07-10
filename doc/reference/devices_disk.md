(devices-disk)=
# Type: `disk`

```{youtube} https://www.youtube.com/watch?v=JhRw2OYTgtg
```

```{note}
The `disk` device type is supported for both containers and VMs.
It supports hotplugging for both containers and VMs.
```

Disk devices supply additional storage to instances.

For containers, they are essentially mount points inside the instance (either as a bind-mount of an existing file or directory on the host, or, if the source is a block device, a regular mount).
Virtual machines share host-side mounts or directories through `9p` or `virtiofs` (if available), or as VirtIO disks for block-based disks.

(devices-disk-types)=
## Types of disk devices

You can create disk devices from different sources.
The value that you specify for the `source` option specifies the type of disk device that is added.
See {ref}`devices-disk-examples` for more detailed information on how to add each type of disk device.

Storage volume
: The most common type of disk device is a storage volume.
  Specify the storage volume name as the source to add a storage volume as a disk device.

Path on the host
: You can share a path on your host (either a file system or a block device) to your instance.
  Specify the host path as the source to add it as a disk device.

Ceph RBD
: LXD can use Ceph to manage an internal file system for the instance, but if you have an existing, externally managed Ceph RBD that you would like to use for an instance, you can add it by specifying `ceph:<pool_name>/<volume_name>` as the source.

CephFS
: LXD can use Ceph to manage an internal file system for the instance, but if you have an existing, externally managed Ceph file system that you would like to use for an instance, you can add it by specifying `cephfs:<fs_name>/<path>` as the source.

ISO file
: You can add an ISO file as a disk device for a virtual machine by specifying its file path as the source.
  It is added as a ROM device inside the VM.

  This source type is applicable only to VMs.

(vm-cloud-init-config)=
VM `cloud-init`
: You can generate a `cloud-init` configuration ISO from the {config:option}`instance-cloud-init:cloud-init.vendor-data` and {config:option}`instance-cloud-init:cloud-init.user-data` configuration keys and attach it to a virtual machine by specifying `cloud-init:config` as the source.
  The `cloud-init` that is running inside the VM then detects the drive on boot and applies the configuration.

  This source type is applicable only to VMs.

  Adding such a configuration disk might be needed if the VM image that is used includes `cloud-init` but not the `lxd-agent`. This is the case for official Ubuntu images prior to `20.04`. On such images, the following steps enable the LXD agent and thus provide the ability to use `lxc exec` to access the VM:

      lxc init ubuntu-daily:18.04 --vm u1
      lxc config device add u1 config disk source=cloud-init:config
      lxc config set u1 cloud-init.user-data - << EOF
      #cloud-config
      #packages:
      #  - linux-image-virtual-hwe-16.04  # 16.04 GA kernel as a problem with vsock
      runcmd:
        - mount -t 9p config /mnt
        - cd /mnt
        - ./install.sh
        - cd /
        - umount /mnt
        - systemctl start lxd-agent  # XXX: causes a reboot
      EOF
      lxc start --console u1

  Note that for `16.04`, the HWE kernel is required to work around a problem with `vsock` (see the commented out section in the above `cloud-config`).

(devices-disk-initial-config)=
## Initial volume configuration for instance root disk devices

Initial volume configuration allows setting specific configurations for the root disk devices of new instances.
These settings are prefixed with `initial.` and are only applied when the instance is created.
This method allows creating instances that have unique configurations, independent of the default storage pool settings.

For example, you can add an initial volume configuration for {config:option}`storage-zfs-volume-conf:zfs.block_mode` to an existing profile, and this
will then take effect for each new instance you create using this profile:

    lxc profile device set <profile_name> <device_name> initial.zfs.block_mode=true

You can also set an initial configuration directly when creating an instance. For example:

    lxc init <image> <instance_name> --device <device_name>,initial.zfs.block_mode=true

Note that you cannot use initial volume configurations with custom volume options or to set the volume's size.

(devices-disk-options)=
## Device options

`disk` devices have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-disk-device-conf start -->
    :end-before: <!-- config group device-disk-device-conf end -->
```

(devices-disk-examples)=
## Configuration examples

How to add a disk device depends on its {ref}`type <devices-disk-types>`.

Storage volume
: To add a storage volume, specify its name as the `source` of the device:

      lxc config device add <instance_name> <device_name> disk pool=<pool_name> source=<volume_name> [path=<path_in_instance>]

  The path is required for file system volumes, but not for block volumes.

  Alternatively, you can use the [`lxc storage volume attach`](lxc_storage_volume_attach.md) command to {ref}`storage-attach-volume`.
  Both commands use the same mechanism to add a storage volume as a disk device.

Path on the host
: To add a host device, specify the host path as the `source`:

      lxc config device add <instance_name> <device_name> disk source=<path_on_host> [path=<path_in_instance>]

  The path is required for file systems, but not for block devices.

Ceph RBD
: To add an existing Ceph RBD volume, specify its pool and volume name:

      lxc config device add <instance_name> <device_name> disk source=ceph:<pool_name>/<volume_name> ceph.user_name=<user_name> ceph.cluster_name=<cluster_name> [path=<path_in_instance>]

  The path is required for file systems, but not for block devices.

CephFS
: To add an existing CephFS file system, specify its name and path:

      lxc config device add <instance_name> <device_name> disk source=cephfs:<fs_name>/<path> ceph.user_name=<user_name> ceph.cluster_name=<cluster_name> path=<path_in_instance>

ISO file
: To add an ISO file, specify its file path as the `source`:

      lxc config device add <instance_name> <device_name> disk source=<file_path_on_host>

VM `cloud-init`
: To add `cloud-init` configuration, specify `cloud-init:config` as the source:

      lxc config device add <instance_name> <device_name> disk source=cloud-init:config

See {ref}`instances-configure-devices` for more information.
