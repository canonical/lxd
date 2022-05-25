# About storage pools and storage volumes
LXD supports creating and managing storage pools and storage volumes.
The following storage drivers are supported:

- {ref}`storage-dir`
- {ref}`storage-ceph`
- {ref}`storage-cephfs`
- {ref}`storage-btrfs`
- {ref}`storage-lvm`
- {ref}`storage-zfs`

## Storage volume content types
Storage volumes can be either `filesystem` or `block` type.

Containers and container images are always going to be using `filesystem`.
Virtual machines and virtual machine images are always going to be using `block`.

Custom storage volumes can be either types with the default being `filesystem`.
Those custom storage volumes of type `block` can only be attached to virtual machines.

Block custom storage volumes can be created with:

```bash
lxc storage volume create [<remote>]:<pool> <name> --type=block
```

## Where to store LXD data
Depending on the storage backends used, LXD can either share the filesystem with its host or keep its data separate.

### Sharing with the host
This is usually the most space efficient way to run LXD and possibly the easiest to manage.
It can be done with:

 - `dir` backend on any backing filesystem
 - `btrfs` backend if the host is Btrfs and you point LXD to a dedicated subvolume
 - `zfs` backend if the host is ZFS and you point LXD to a dedicated dataset on your zpool

### Dedicated disk/partition
In this mode, LXD's storage will be completely independent from the host.
This can be done by having LXD use an empty partition on your main disk or by having it use a full dedicated disk.

This is supported by all storage drivers except `dir`, `ceph` and `cephfs`.

### Loop disk
If neither of the options above are possible for you, LXD can create a loop file
on your main drive and then have the selected storage driver use that.

This is functionally similar to using a disk/partition but uses a large file on your main drive instead.
This comes at a performance penalty as every writes need to go through the storage driver and then your main
drive's filesystem. The loop files also usually cannot be shrunk.
They will grow up to the limit you select but deleting instances or images will not cause the file to shrink.

## Default storage pool
There is no concept of a default storage pool in LXD.
Instead, the pool to use for the instance's root is treated as just another "disk" device in LXD.

The device entry looks like:

```yaml
  root:
    type: disk
    path: /
    pool: default
```

And it can be directly set on an instance (`--storage` option to "lxc launch" and "lxc init")
or it can be set through LXD profiles.

That latter option is what the default LXD setup (through "lxd init") will do for you.
The same can be done manually against any profile using (for the "default" profile):

```bash
lxc profile device add default root disk path=/ pool=default
```
