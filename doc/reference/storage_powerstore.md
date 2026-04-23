(storage-powerstore)=
# Dell PowerStore - `powerstore`

Dell PowerStore is a storage solution from [Dell Technologies](https://www.dell.com/).
It offers the consumption of block storage across the network.

LXD supports connecting to PowerStore storage through {abbr}`iSCSI (Internet Small Computer Systems Interface)`.

Ensure that the required kernel modules and the iSCSI CLI (`iscsiadm`) are installed on your host system.

## Terminology

PowerStore does not have a concept of storage pools.
Instead, LXD scopes its volumes to a storage pool by prefixing each volume name with a deterministic storage pool identifier.
This prefix prevents name conflicts between volumes belonging to different LXD storage pools on the same PowerStore array.

LXD creates volumes on the PowerStore array and maps them to the respective LXD host.
When the first volume needs to be mapped to a specific LXD host, LXD discovers and connects to the available targets provided by PowerStore.

## The `powerstore` driver in LXD

The `powerstore` driver in LXD uses PowerStore volumes for custom storage volumes, instances, and snapshots.
All created volumes are thin-provisioned block volumes. If required (for example, for containers and custom file system volumes), LXD formats the volume with a desired file system.

LXD expects PowerStore to be pre-configured and made accessible to LXD by specifying authentication credentials during storage pool creation. Furthermore, LXD assumes that it has full control over the volumes it manages.

This driver provides remote storage.
As a result, and depending on the internal network, storage access might be a bit slower compared to local storage.
On the other hand, using remote storage has significant advantages in a cluster setup: all cluster members have access to the same storage pools with the exact same contents, without the need to synchronize them.

When a volume is first mapped to the LXD host, LXD discovers the available targets from the PowerStore array and connects to them.
Alternatively, you can specify which targets to use with {config:option}`storage-powerstore-pool-conf:powerstore.target`.

Volume snapshots are supported by PowerStore. When a volume with at least one snapshot is copied, LXD sequentially copies snapshots into the destination volume, from which a new snapshot is created. Finally, once all snapshots are copied, the source volume is copied into the destination volume.

(storage-powerstore-volume-names)=
### Volume names

The driver uses the volume's {config:option}`storage-powerstore-volume-conf:volatile.uuid` to generate a volume name.
As described in the [terminology section](storage-powerstore), the pool-scoped prefix `lxd-<pool_name_hash>-` is prepended to all volume names.

For example, a UUID `5a2504b0-6a6c-4849-8ee7-ddb0b674fd14` is used as the base of the volume name.
To distinguish volume types, special identifiers are prepended and appended to the volume names, as depicted in the table below:

Type                       | Identifier   | Example
:--                        | :---         | :----------
Container                  | `c_`         | `c_5a2504b0-6a6c-4849-8ee7-ddb0b674fd14`
Virtual machine            | `v_`         | `v_5a2504b0-6a6c-4849-8ee7-ddb0b674fd14.b` (block volume) and `v_5a2504b0-6a6c-4849-8ee7-ddb0b674fd14` (file system volume)
Image (ISO)                | `i_`         | `i_5a2504b0-6a6c-4849-8ee7-ddb0b674fd14.i`
Custom volume              | `u_`         | `u_5a2504b0-6a6c-4849-8ee7-ddb0b674fd14` (file system volume) and `u_5a2504b0-6a6c-4849-8ee7-ddb0b674fd14.b` (block volume)
Mountable snapshot clone   | `s`          | `sc_5a2504b0-6a6c-4849-8ee7-ddb0b674fd14` (container), `sv_5a2504b0-6a6c-4849-8ee7-ddb0b674fd14.b` (VM) and `su_5a2504b0-6a6c-4849-8ee7-ddb0b674fd14` (custom volume)

Snapshots in PowerStore are native children of their parent volume. Each snapshot is named using the snapshot's own UUID with the same type prefix as the parent volume.
Mountable snapshot clones are temporary volumes created by LXD when a snapshot needs to be directly accessed (for example, during export). The `s` prefix is prepended to the volume type identifier to distinguish them from regular volumes.

(storage-powerstore-limitations)=
### Limitations

The `powerstore` driver has the following limitations:

Volume size constraints
: The minimum volume size (quota) is `1MiB` and must be a multiple of `1MiB`. The maximum volume size is `256TiB`.

Volume shrinking
: The PowerStore driver does not allow shrinking volumes.

Sharing custom volumes between instances
: The PowerStore driver "simulates" volumes with content type `filesystem` by putting a file system on top of a PowerStore volume.
  Therefore, custom storage volumes can only be assigned to a single instance at a time.

Sharing a PowerStore storage pool between multiple LXD installations
: Sharing the same PowerStore storage pool between multiple LXD installations is not supported.

Recovering PowerStore storage pools
: Recovery of PowerStore storage pools using `lxd recover` is not supported.

(storage-powerstore-options)=
## Configuration options

The following configuration options are available for storage pools that use the `powerstore` driver, as well as storage volumes in these pools.

(storage-powerstore-pool-config)=
### Storage pool configuration

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group storage-powerstore-pool-conf start -->
    :end-before: <!-- config group storage-powerstore-pool-conf end -->
```

{{volume_configuration}}

(storage-powerstore-vol-config)=
### Storage volume configuration

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group storage-powerstore-volume-conf start -->
    :end-before: <!-- config group storage-powerstore-volume-conf end -->
```
