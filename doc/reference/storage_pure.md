(storage-pure)=
# Everpure - `pure`

[Everpure](https://www.everpuredata.com/) (formerly known as Pure Storage) is an all-flash storage platform. It offers the consumption of redundant block storage across the network.

LXD supports connecting to Everpure FlashArrays through four modes: {abbr}`iSCSI (Internet Small Computer Systems Interface)` (`iscsi`), {abbr}`NVMe/TCP (Non-Volatile Memory Express over Transmission Control Protocol)` (`nvme/tcp`), NVMe over {abbr}`FC (Fibre Channel)` (`nvme/fc`), and SCSI over FC (`scsi/fc`).
In addition, Everpure offers copy-on-write snapshots, thin provisioning, and other features.

To use Everpure with LXD requires an Everpure FlashArray API version of at least `2.21`, corresponding to a minimum Purity//FA version of `6.4.2`.

Additionally, ensure that the required kernel modules for the selected mode are installed on your host system.
For iSCSI, the iSCSI CLI named `iscsiadm` needs to be installed in addition to the required kernel modules.
For both Fibre Channel modes, a Fibre Channel {abbr}`HBA (host bus adapter)` that is zoned to the array is required.
For `scsi/fc`, the `scsi_transport_fc` kernel module is needed, and volumes are always accessed through multipath, so `multipath-tools` must be installed and the multipath daemon must be running.
For `nvme/fc`, the `nvme_fc` kernel module and the `nvme` CLI are needed, and multipath is handled by the NVMe subsystem rather than by `multipath-tools`.

## Terminology

Each storage pool created in LXD using an Everpure driver represents an Everpure *pod*, which is an abstraction that groups multiple volumes under a specific name.
One benefit of using Everpure pods is that they can be linked with multiple Everpure FlashArrays to provide additional redundancy.

LXD creates volumes within a pod that is identified by the storage pool name.
When the first volume needs to be mapped to a specific LXD host, a corresponding Everpure host is created with the name of the LXD host and a suffix of the used protocol.
For example, if the LXD host is `host01` and the mode is `nvme/tcp`, the resulting Everpure host would be `host01-nvme-tcp`.
Likewise, in `scsi/fc` mode the resulting Everpure host would be `host01-scsi-fc`, because a slash (`/`) is not valid in a host name and is therefore replaced with a hyphen (`-`).

The Everpure host is then connected with the required volumes, to allow attaching and accessing volumes from the LXD host.
The created Everpure host is automatically removed once there are no volumes connected to it.

## The `pure` driver in LXD

The `pure` driver in LXD uses Everpure volumes for custom storage volumes, instances, and snapshots.
All created volumes are thin-provisioned block volumes. If required (for example, for containers and custom file system volumes), LXD formats the volume with a desired file system.

LXD expects Everpure to be pre-configured with a specific service (e.g. iSCSI) on network interfaces whose address is provided during storage pool configuration.
This does not apply in the Fibre Channel modes, because Fibre Channel target ports are not network interfaces.
Furthermore, LXD assumes that it has full control over the Everpure pods it manages.
Therefore, you should never maintain any volumes in Everpure pods that are not owned by LXD because LXD might disconnect or even delete them.

This driver behaves differently than some of the other drivers in that it provides remote storage.
As a result, and depending on the internal network, storage access might be a bit slower compared to local storage.
On the other hand, using remote storage has significant advantages in a cluster setup: all cluster members have access to the same storage pools with the exact same contents, without the need to synchronize them.

When creating a new storage pool using the `pure` driver in either `iscsi` or `nvme/tcp` mode, LXD automatically discovers the array's qualified name and target address (portal).
In the Fibre Channel modes, LXD instead discovers the online Fibre Channel target ports through the local host bus adapter, because Fibre Channel targets are addressed by {abbr}`WWN (World Wide Name)` rather than by network address.
In `scsi/fc` mode, a target is the port's {abbr}`WWPN (World Wide Port Name)` itself, whereas in `nvme/fc` mode the target is the array's subsystem {abbr}`NQN (NVMe Qualified Name)` reached at a Fibre Channel transport address that is derived from the port's WWN.
An array can present Fibre Channel ports for both modes at the same time, so LXD selects ports that correspond to the configured mode: for `scsi/fc` those reporting a WWN and no NQN, and for `nvme/fc` those reporting both.
Consequently, {config:option}`storage-pure-pool-conf:pure.target` has no effect in either Fibre Channel mode, and, instead, fabric zoning determines which targets are reachable.

Upon successful discovery, LXD attaches all volumes that are connected to the Everpure host that is associated with a specific LXD server.
Everpure hosts and volume connections are fully managed by LXD.

Volume snapshots are also supported by Everpure.
When a volume with at least one snapshot is copied, LXD sequentially creates snapshots on the destination volume from snapshots on the source volume.
Each snapshot is associated with a parent volume and cannot be directly attached to the host; therefore, when a snapshot is exported, LXD creates a temporary volume behind the scenes.
This volume is attached to the LXD host and removed once the operation is complete.
Finally, once all snapshots are copied, the source volume is copied into the destination volume.

(storage-pure-volume-names)=
### Volume names

Due to a [limitation](storage-pure-limitations) in Everpure, volume names cannot exceed 63 characters.
Therefore, the driver uses the volume's {config:option}`storage-pure-volume-conf:volatile.uuid` to generate a shorter volume name.

For example, a UUID `5a2504b0-6a6c-4849-8ee7-ddb0b674fd14` is first trimmed of any hyphens (`-`), resulting in the string `5a2504b06a6c48498ee7ddb0b674fd14`.
To distinguish volume types and snapshots, special identifiers are prepended and appended to the volume names, as depicted in the table below:

Type            | Identifier   | Example
:--             | :---         | :----------
Container       | `c-`         | `c-5a2504b06a6c48498ee7ddb0b674fd14`
Virtual machine | `v-`         | `v-5a2504b06a6c48498ee7ddb0b674fd14-b` (block volume) and `v-5a2504b06a6c48498ee7ddb0b674fd14` (file system volume)
Image           | `i-`         | `i-5a2504b06a6c48498ee7ddb0b674fd14` (file system image) and `i-5a2504b06a6c48498ee7ddb0b674fd14-b` (block image)
Custom volume   | `u-`         | `u-5a2504b06a6c48498ee7ddb0b674fd14` (file system volume), `u-5a2504b06a6c48498ee7ddb0b674fd14-b` (block volume) and `u-5a2504b06a6c48498ee7ddb0b674fd14-i` (ISO volume)
Snapshot        | `s`          | `sc-5a2504b06a6c48498ee7ddb0b674fd14` (container snapshot), `sv-5a2504b06a6c48498ee7ddb0b674fd14-b` (VM snapshot) and `su-5a2504b06a6c48498ee7ddb0b674fd14` (custom volume snapshot)

(storage-pure-limitations)=
### Limitations

The `pure` driver has the following limitations:

Volume size constraints
: Minimum volume size (quota) is `1MiB` and must be a multiple of `512B`. If the requested size does not meet these conditions, LXD automatically rounds it up to the nearest valid value.

Snapshots cannot be mounted
: Snapshots cannot be mounted directly to the host. Instead, a temporary volume must be created to access the snapshot's contents.
  For internal operations, such as copying instances or exporting snapshots, LXD handles this automatically.

Sharing the Everpure storage pool between multiple LXD installations
: Sharing an Everpure FlashArray between multiple LXD installations is possible provided that installations use distinct storage pool names. Storage pools are implemented as Pods on the array and pod names have to be unique.

Recovering Everpure storage pools
: Recovery of Everpure storage pools using `lxd recover` is currently not supported.

## Configuration options

The following configuration options are available for storage pools that use the `pure` driver, as well as storage volumes in these pools.

(storage-pure-pool-config)=
### Storage pool configuration

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group storage-pure-pool-conf start -->
    :end-before: <!-- config group storage-pure-pool-conf end -->
```

{{volume_configuration}}

(storage-pure-vol-config)=
### Storage volume configuration

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group storage-pure-volume-conf start -->
    :end-before: <!-- config group storage-pure-volume-conf end -->
```
