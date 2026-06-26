---
myst:
  html_meta:
    description: Release notes for LXD 5.21.5, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-5.21.5)=
# LXD 5.21.5 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 5.21 LTS series. It backports a number of new features, storage and networking improvements, security hardening, and bug fixes from the main development branch.

(ref-release-notes-5.21.5-highlights)=
## Highlights

This section highlights new and improved features in this release.

### HPE Alletra storage driver

A new `alletra` storage driver has been added for the consumption of storage volumes from an HPE Alletra storage array. The driver supports both iSCSI and NVMe/TCP connections, including volume resize and multipath handling.

- Documentation: {ref}`storage-alletra`
- API extension: {ref}`extension-storage-driver-alletra`

### OVN internal load balancers and network forwards

Support for internal OVN load balancers and network forwards has been introduced. This allows `ovn` networks to define ports on internal IP addresses that can be forwarded to other internal IPs within their networks, removing the previous limitation that load balancers and network forwards could only forward from external IP addresses.

- Documentation: {ref}`network-load-balancers` and {ref}`network-forwards`
- API extension: {ref}`extension-ovn-internal-load-balancer`

### OVN DHCP ranges

Support for the {config:option}`network-ovn-network-conf:ipv4.dhcp.ranges` configuration key has been added for `ovn` networks, allowing a list of IPv4 ranges to be reserved for dynamic allocation using DHCP.

- Documentation: {ref}`network-ovn-setup`
- API extension: {ref}`extension-ovn-dhcp-ranges`

### OVN NIC acceleration parent

Support has been added for specifying the OVN NIC acceleration physical function interfaces from which to allocate virtual functions. This avoids the need to add physical function interfaces to the OVN integration bridge.

- Documentation: {ref}`nic-ovn`
- API extension: {ref}`extension-ovn-nic-acceleration-parent`

### Forced project deletion

Support has been added for force deleting projects together with their entities (instances, profiles, images, networks, network ACLs, network zones, storage volumes, and storage buckets) by setting the `force` query parameter on `DELETE /1.0/projects/{name}` requests.

- API extension: {ref}`extension-projects-force-delete`

### Importing custom volumes from tarballs

A new `tar` option has been added for the `--type` parameter in the `POST /1.0/storage-pools/{poolName}/volumes/{type}` API call.

- Documentation: {ref}`howto-storage-volumes`
- API extension: {ref}`extension-import-custom-volume-tar`

### Persistent VM PCIe bus allocations

Support has been added for persistently recording VM PCIe bus allocations in `volatile.<name>.bus` configuration keys, improving the stability of device addressing across VM restarts.

- API extension: {ref}`extension-vm-persistent-bus`

### Operation requestor information

A new `requestor` field has been added to operations, which contains information about the caller that initiated the operation.

- API extension: {ref}`extension-operation-requestor`

### Disk usage in resources

A `used_by` field has been added to disks returned by the resources endpoint to indicate their use by any virtual parent device, for example `bcache`.

- API extension: {ref}`extension-resources-disk-used-by`

(ref-release-notes-5.21.5-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Project restriction bypass in instance copy across projects (CVE-2026-55622)`](https://github.com/canonical/lxd/security/advisories/GHSA-qx75-2p3r-pwm5)
- [{spellexception}`Project restriction bypass for custom volume copy across projects (CVE-2026-55621)`](https://github.com/canonical/lxd/security/advisories/GHSA-7mr3-28h5-m5vx)
- [{spellexception}`Restricted project bypass leading to arbitrary command execution (CVE-2026-48751)`](https://github.com/canonical/lxd/security/advisories/GHSA-47w9-6r3f-938g)
- [{spellexception}`Arbitrary file write on host via `exec-output` symlink in crafted image (CVE-2026-48750)`](https://github.com/canonical/lxd/security/advisories/GHSA-9j25-mm2h-2f76)
- [{spellexception}`Arbitrary file read+write on host via templates/ symlink in malicious image (CVE-2026-48752)`](https://github.com/canonical/lxd/security/advisories/GHSA-jpf8-86f3-wp38)
- [{spellexception}`Arbitrary file read+write on host via rootfs/ symlink in malicious image (CVE-2026-48749)`](https://github.com/canonical/lxd/security/advisories/GHSA-vghh-5rfx-xhq8)
- [{spellexception}`Argument injection in backup compression algorithm leading to AFW and ACE (CVE-2026-48755)`](https://github.com/canonical/lxd/security/advisories/GHSA-fmc8-p6q7-75cc)
- [{spellexception}`Arbitrary file write on client due to trusted image hash (CVE-2026-48769)`](https://github.com/canonical/lxd/security/advisories/GHSA-pjff-c2wc-f6jm)
- [{spellexception}`CreateCustomVolumeFromBackup nil-pointer dereference on volumes[0].snapshots[*].expires_at (CVE-2026-9639)`](https://github.com/canonical/lxd/security/advisories/GHSA-j93m-3j9p-m5m8)
- [{spellexception}`Backup snapshot import bypasses project restrictions (CVE-2026-9640)`](https://github.com/canonical/lxd/security/advisories/GHSA-ppq7-4492-5552)
- [{spellexception}`Fix CephFS volume multi-use`](https://github.com/canonical/lxd/pull/18509)
- [{spellexception}`Fix local config being wiped out by reverter`](https://github.com/canonical/lxd/pull/18186)
- [{spellexception}`Fix in-memory config corruption on update failure`](https://github.com/canonical/lxd/pull/17198)
- [{spellexception}`Fix potential crash if non string config sent as notification in api10Put`](https://github.com/canonical/lxd/pull/18186)
- [{spellexception}`Fix deadlock by only taking storage pool and network creation lock for external API requests`](https://github.com/canonical/lxd/pull/18122)
- [{spellexception}`Fix nil pointer dereference in instance backup restore`](https://github.com/canonical/lxd/pull/18255)
- [{spellexception}`Validate snapshot.ExpiresAt is non-nil`](https://github.com/canonical/lxd/pull/18390)
- [{spellexception}`Fix instance migration with pool/project/target changes`](https://github.com/canonical/lxd/pull/18093)
- [{spellexception}`Fix remote cluster migration`](https://github.com/canonical/lxd/pull/17086)
- [{spellexception}`Fix cluster healing functionality`](https://github.com/canonical/lxd/pull/18152)
- [{spellexception}`Validate a cluster group edit does not ignore a member removal`](https://github.com/canonical/lxd/pull/17023)
- [{spellexception}`Fix effective project handling for used-by lists`](https://github.com/canonical/lxd/pull/17023)
- [{spellexception}`Fix project for warning lifecycle events`](https://github.com/canonical/lxd/pull/17074)
- [{spellexception}`Fix missing requestor on the project API`](https://github.com/canonical/lxd/pull/17074)
- [{spellexception}`Fix pruneExpiredImages() to correctly track images`](https://github.com/canonical/lxd/pull/17086)
- [{spellexception}`Fix fingerprint arg usage in the images API`](https://github.com/canonical/lxd/pull/17074)
- [{spellexception}`Fix bad snapshot index calculation in the storage backend`](https://github.com/canonical/lxd/pull/18255)
- [{spellexception}`Fix storage patch update with instance volume with size config`](https://github.com/canonical/lxd/pull/16403)
- [{spellexception}`Unmap block device for ISO volumes`](https://github.com/canonical/lxd/pull/16378)
- [{spellexception}`Round Pure Storage volume size to make it divisible by 512B`](https://github.com/canonical/lxd/pull/16356)
- [{spellexception}`Ensure Pure Storage image size is applied if not specified otherwise`](https://github.com/canonical/lxd/pull/16339)
- [{spellexception}`Fix Alletra and Pure block device unmap and volume resize in iSCSI/multipath mode`](https://github.com/canonical/lxd/pull/17074)
- [{spellexception}`Fix NVMe Discovery() to filter out invalid NQNs`](https://github.com/canonical/lxd/pull/16378)
- [{spellexception}`Fix lxc auth regression where groups were not removed from identities`](https://github.com/canonical/lxd/pull/17080)
- [{spellexception}`Fix crash on nil qmp handler in RunJSON`](https://github.com/canonical/lxd/pull/18252)
- [{spellexception}`Fix race condition in simpleListenerConnection.WriteJSON()`](https://github.com/canonical/lxd/pull/17086)
- [{spellexception}`Explicitly close both ends of mirrored websockets to avoid hanging instance console websockets`](https://github.com/canonical/lxd/pull/17074)
- [{spellexception}`Fix stale CDI-related files cleanup logic for physical GPU devices`](https://github.com/canonical/lxd/pull/16611)
- [{spellexception}`Fix getDHCPv4Reservations() and randomAddressInSubnet() in the network drivers`](https://github.com/canonical/lxd/pull/17080)
- [{spellexception}`Fix IsNetworkPortRange() to respect ports being 16 bits`](https://github.com/canonical/lxd/pull/17023)
- [{spellexception}`Make /proc/cpuinfo parser less strict`](https://github.com/canonical/lxd/pull/16662)
- [{spellexception}`Fix file push owner/group change not working on VMs`](https://github.com/canonical/lxd/pull/16331)
- [{spellexception}`Use uint64 for interface statistics counters`](https://github.com/canonical/lxd/pull/17086)
- [{spellexception}`Fix incorrect address comparison when changing the pprof address`](https://github.com/canonical/lxd/pull/18186)
- [{spellexception}`Fix goroutine hangs in events handling`](https://github.com/canonical/lxd/pull/18402)

(ref-release-notes-5.21.5-incompatible)=
## Backwards-incompatible changes

These changes are not compatible with older versions of LXD or its clients.

### Minimum system requirement changes

The minimum supported version of some components has changed:

- The minimum required version of Go to build LXD is now 1.25.11 (see [Updated minimum Go version](#ref-release-notes-5.21.5-go)).

### Stricter validation and tightened permissions

Several inputs are now validated more strictly, and some permissions have been tightened as part of security hardening backports. Requests that previously succeeded with malformed or unexpected values may now be rejected:

- Stricter certificate fingerprint validation.
- Stricter checks for low-level (`raw.*`) configuration options.
- Improved certificate edit validation.
- Tightened storage pool permissions.
- Validation of struct slices and config during import.

(ref-release-notes-5.21.5-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.25.11 (previously 1.24.5).

(ref-release-notes-5.21.5-snap)=
## Snap packaging changes

- Transitioned the snap base from `core22` to `core24`.
- Several bundled components are now staged from the Ubuntu archive or built from Ubuntu source packages instead of being built from upstream Git, reducing build complexity. This includes Open vSwitch, OVN, swtpm, virtiofsd, and squashfs-tools-ng.
- QEMU is now built from the Ubuntu source package (`8.2.2+ds-0ubuntu1.17`) instead of upstream Git.
- EDK2/OVMF is now built from the Ubuntu source package (`2024.02-2ubuntu0.8`) instead of upstream Git.
- SPICE is now built from the Ubuntu source package (`0.15.1-1build2`) instead of upstream Git.
- Enabled LXCFS per-container process tracking (`snap set lxd lxcfs.pidfd=true`) by default.
- dqlite bumped to v1.17.3.
- LXC bumped to v6.0.6.
- LXCFS bumped to v6.0.6.
- LXCFS: Reverted partial backport of PSI functionality that prevented host machine suspend (#17983).
- libnvidia-container bumped to v1.19.0.
- NVIDIA container toolkit bumped to v1.19.0.
- ZFS 2.2 bumped to 2.2.9.
- ZFS 2.3 bumped to 2.3.6.
- ZFS 2.4 bumped to 2.4.1.

(ref-release-notes-5.21.5-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-5.21.4...lxd-5.21.5).

(ref-release-notes-5.21.5-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-5.21.5).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=5.21/stable`
- **MacOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
