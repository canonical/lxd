---
myst:
  html_meta:
    robots: noindex
    description: Reference documentation for LXD feature previews, including how to enable and disable unsupported work-in-progress features.
---

(ref-feature-previews)=
# Feature previews

```{warning}
**Do not** enable feature previews in a production environment.
Feature previews enable unsupported and unfinished functionality, which might cause unwanted or unpredictable system behavior, performance degradation, or data loss/corruption and more.
```

Feature previews provide access to work-in-progress features.
All previews are disabled by default.

(ref-feature-previews-configure-snap)=
## Configure feature previews with the snap

To enable one or more feature previews, set the snap `features` option to a comma-separated list of feature names:

```bash
sudo snap set lxd features=feature_name,another_feature
```

To disable all feature previews, run:

```bash
sudo snap unset lxd features
```

Restart the LXD daemon to apply feature preview changes to the server.
Use the `--reload` flag to keep instances running (see {ref}`howto-snap-daemon`):

```bash
sudo snap restart --reload lxd
```

For more information about snap configuration options, visit {ref}`snap:how-to-guides-work-with-snaps-configure-snaps` in the Snap documentation.

(ref-feature-previews-active)=
## Active feature previews

(ref-feature-previews-changed-block-tracking)=
### Changed block tracking

- Feature name: `changed_block_tracking`

Changed block tracking records the blocks of a block volume that a guest writes to after a snapshot.
This enables a backup tool to copy only those blocks instead of the whole volume.

To use changed block tracking, see {ref}`howto-storage-block-tracking`.

(ref-feature-previews-microvms)=
### MicroVM instances

- Feature name: `microvm`

MicroVMs boot directly from a kernel image provided by the host and run container images unpacked into an ext4 filesystem on a dedicated block volume.
MicroVMs have only been tested on `x86_64` hosts.

MicroVMs can currently be created on storage pools that use the `dir`, `btrfs`, `zfs`, `lvm` or `ceph` driver.
Cached image volumes are not used; the image is unpacked for every new MicroVM.

To create MicroVMs, you must install and build [libkrun](https://github.com/libkrun/libkrun) and [libkrunfw](https://github.com/libkrun/libkrunfw), and enable the `microvm` feature preview.
In a cluster, you must enable the `microvm` feature preview on all cluster members.

1. First, install the required build tools.

   Install the Rust toolchain:

   ```bash
   sudo snap install rustup --classic
   rustup default stable
   rustup target add $(uname -m)-unknown-linux-musl
   ```

   Install the packages required to build libkrun and libkrunfw:

   ```bash
   sudo apt install python3-pyelftools build-essential flex bison libelf-dev
   ```

2. Next, build libkrun with block and network support.
   LXD uses the libkrun C API, which later libkrun revisions no longer provide, so check out the following compatible revision:

   ```bash
   git clone https://github.com/libkrun/libkrun.git
   git -C libkrun checkout 6254f5e22b8782a4dffe530ff44f1bbe623332e0
   make -C libkrun BLK=1 NET=1 -j"$(nproc)"
   ```

3. Build a guest kernel, for example with [`libkrunfw`](https://github.com/libkrun/libkrunfw):

   ```bash
   git clone https://github.com/libkrun/libkrunfw.git
   make -C libkrunfw -j"$(nproc)"
   ```

4. Copy the uncompressed kernel and the libkrun library to the `libkrun` directory of LXD (`/var/snap/lxd/common/lxd/libkrun` for the snap):

   ```bash
   sudo mkdir -p /var/snap/lxd/common/lxd/libkrun
   sudo cp libkrunfw/linux-*/vmlinux /var/snap/lxd/common/lxd/libkrun/vmlinuz
   sudo cp libkrun/target/release/libkrun.so /var/snap/lxd/common/lxd/libkrun/
   ```

5. {ref}`Configure the snap <ref-feature-previews-configure-snap>` to enable the `microvm` feature preview and restart the daemon.

6. You can now use the `--microvm` flag to create a MicroVM from a container image:

   ```bash
   lxc launch ubuntu:26.04 m1 --microvm
   ```
