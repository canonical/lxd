(storage-alletra)=
# HPE Alletra - `alletra`

[HPE Alletra](https://www.hpe.com/emea_europe/en/hpe-alletra.html) is a storage solution. It offers the consumption of redundant block storage across the network.

LXD supports connecting to HPE Alletra storage through {abbr}`NVMe/TCP (Non-Volatile Memory Express over Transmission Control Protocol)`.
In addition, HPE Alletra offers copy-on-write snapshots, thin provisioning, and other features.

Using HPE Alletra with LXD requires a HPE Alletra WSAPI version `1`. Additionally, ensure that the required kernel modules for the selected protocol are installed on your host system.

(storage-alletra-terminology)=
## Terminology

Each storage pool created in LXD using an HPE Alletra driver represents an HPE Alletra *volume set*, which is an abstraction that groups multiple volumes under a specific name.

LXD creates volumes within a volume set that is identified by the storage pool name.
When the first volume needs to be mapped to a specific LXD host, a corresponding HPE Alletra host entity is created with the name of the LXD host and a suffix of the used protocol.
For example, if the LXD host is `host01` and the mode is `nvme`, the resulting HPE Alletra host entity would be `host01-nvme`.

The HPE Alletra host is then connected with the required volumes to allow attaching and accessing volumes from the LXD host.
The HPE Alletra host is automatically removed once there are no volumes connected to it.

(storage-alletra-driver)=
## The `alletra` driver in LXD

The `alletra` driver in LXD uses HPE Alletra volumes for custom storage volumes, instances, and snapshots.
All created volumes are thin-provisioned block volumes. If required (for example, for containers and custom file system volumes), LXD formats the volume with a desired file system.

LXD expects HPE Alletra to be pre-configured with a specific service (such as iSCSI) on the network interfaces whose addresses you provide during storage pool configuration.
Furthermore, LXD assumes that it has full control over the HPE Alletra volume sets it manages.
Therefore, do not keep any volumes in HPE Alletra volume sets unless they are owned by LXD, because LXD might disconnect or even delete them.

This driver provides remote storage.
As a result, and depending on the internal network, storage access might be a bit slower compared to local storage.
On the other hand, using remote storage has significant advantages in a cluster setup: all cluster members have access to the same storage pools with the exact same contents, without the need to synchronize them.

When creating a new storage pool using the `alletra` driver, LXD automatically discovers the array's qualified name and target address.
Upon successful discovery, LXD attaches all volumes that are connected to the HPE Alletra host that is associated with a specific LXD server.
HPE Alletra hosts and volume connections ({abbr}`vLUNs (virtual Logical Unit Numbers)`) are fully managed by LXD.

Volume snapshots are also supported by HPE Alletra. When a volume with at least one snapshot is copied, LXD sequentially copies snapshots into the destination volume, from which a new snapshot is created. Finally, once all snapshots are copied, the source volume is copied into the destination volume.

(storage-alletra-volume-names)=
### Volume names

As a Pure storage driver, the `alletra` driver uses the volume's {config:option}`storage-alletra-volume-conf:volatile.uuid` to generate a volume name.

For example, a UUID `5a2504b0-6a6c-4849-8ee7-ddb0b674fd14` is first trimmed of any hyphens (`-`), resulting in the string `5a2504b06a6c48498ee7ddb0b674fd14`.
To distinguish volume types and snapshots, special identifiers are prepended and appended to the volume names, as depicted in the table below:

Type            | Identifier   | Example
:--             | :---         | :----------
Container       | `c-`         | `c-5a2504b06a6c48498ee7ddb0b674fd14`
Virtual machine | `v-`         | `v-5a2504b06a6c48498ee7ddb0b674fd14-b` (block volume) and `v-5a2504b06a6c48498ee7ddb0b674fd14` (file system volume)
Image (ISO)     | `i-`         | `i-5a2504b06a6c48498ee7ddb0b674fd14-i`
Custom volume   | `u-`         | `u-5a2504b06a6c48498ee7ddb0b674fd14` (file system volume) and `u-5a2504b06a6c48498ee7ddb0b674fd14-b` (block volume)
Snapshot        | `s`          | `sc-5a2504b06a6c48498ee7ddb0b674fd14` (container snapshot), `sv-5a2504b06a6c48498ee7ddb0b674fd14-b` (VM snapshot) and `su-5a2504b06a6c48498ee7ddb0b674fd14` (custom volume snapshot)

(storage-alletra-limitations)=
### Limitations

The `alletra` driver has the following limitations:

Volume size constraints
: The minimum volume size (quota) is `256MiB` and must be a multiple of `256MiB`. If the requested size does not meet these conditions, LXD automatically rounds it up to the nearest valid value.

Sharing an HPE Alletra storage pool between multiple LXD installations
: Sharing an HPE Alletra array among multiple LXD installations is possible, provided that the installations use distinct storage pool names. Storage pools are implemented as volume sets on the array, and volume set names must be unique.

Recovering HPE Alletra storage pools
: Recovery of HPE Alletra storage pools using `lxd recover` is currently not supported.

(storage-alletra-options)=
## Configuration options

The following configuration options are available for storage pools that use the `alletra` driver, as well as storage volumes in these pools.

(storage-alletra-pool-config)=
### Storage pool configuration

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group storage-alletra-pool-conf start -->
    :end-before: <!-- config group storage-alletra-pool-conf end -->
```

{{volume_configuration}}

(storage-alletra-vol-config)=
### Storage volume configuration

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group storage-alletra-volume-conf start -->
    :end-before: <!-- config group storage-alletra-volume-conf end -->
```
