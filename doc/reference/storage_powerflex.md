(storage-powerflex)=
# Dell PowerFlex - `powerflex`

[Dell PowerFlex](https://www.dell.com/en-us/shop/powerflex/sf/powerflex) is a software-defined storage solution from [Dell Technologies](https://www.dell.com/). Among other things it offers the consumption of redundant block storage across the network.

LXD offers access to PowerFlex storage clusters using either NVMe/TCP or Dell's Storage Data Client (SDC).
In addition, PowerFlex offers copy-on-write snapshots, thin provisioning and other features.

To use PowerFlex with NVMe/TCP, make sure you have the required kernel modules installed on your host system.
On Ubuntu these are `nvme_fabrics` and `nvme_tcp`, which come bundled in the `linux-modules-extra-$(uname -r)` package.
LXD takes care of connecting to the respective subsystem.

When using the SDC, LXD requires it to already be connected to the Dell Metadata Manager (MDM) beforehand.
As LXD doesn't set up the SDC, follow the official guides from Dell for configuration details.

## Terminology

PowerFlex groups various so-called {abbr}`SDS (storage data servers)` under logical groups within a protection domain.
Those SDS are the hosts that contribute storage capacity to the PowerFlex cluster.
A *protection domain* contains storage pools, which represent a set of physical storage devices from different SDS.
LXD creates its volumes in those storage pools.

You can take a snapshot of any volume in PowerFlex, which will create an independent copy of the parent volume.
PowerFlex volumes get added as a drive to the respective LXD host the volume got mapped to.
In case of NVMe/TCP, the LXD host connects to one or multiple NVMe {abbr}`SDT (storage data targets)` provided by PowerFlex.
Those SDT run as components on the PowerFlex storage layer.
In case of SDC, the LXD hosts don't set up any connection by themselves.
Instead they depend on the SDC to make the volumes available on the system for consumption.

## `powerflex` driver in LXD

The `powerflex` driver in LXD uses PowerFlex volumes for custom storage volumes, instances and snapshots.
For storage volumes with content type `filesystem` (containers and custom file-system volumes), the `powerflex` driver uses volumes with a file system on top (see {config:option}`storage-powerflex-volume-conf:block.filesystem`).
By default, LXD creates thin-provisioned PowerFlex volumes.

LXD expects the PowerFlex protection domain and storage pool already to be set up.
Furthermore, LXD assumes that it has full control over the storage pool.
Therefore, you should never maintain any volumes that are not owned by LXD in a PowerFlex storage pool, because LXD might delete them.

This driver behaves differently than some of the other drivers in that it provides remote storage.
As a result and depending on the internal network, storage access might be a bit slower than for local storage.
On the other hand, using remote storage has big advantages in a cluster setup, because all cluster members have access to the same storage pools with the exact same contents, without the need to synchronize storage pools.

When creating a new storage pool using the `powerflex` driver in `nvme` mode, LXD tries to discover one of the SDT from the given storage pool.
Alternatively, you can specify which SDT to use with {config:option}`storage-powerflex-pool-conf:powerflex.sdt`.
LXD instructs the NVMe initiator to connect to all the other SDT when first connecting to the subsystem.

Due to the way copy-on-write works in PowerFlex, snapshots of any volume don't rely on its parent.
As a result, volume snapshots are fully functional volumes themselves, and it's possible to take additional snapshots from such volume snapshots.
This tree of dependencies is called the *PowerFlex vTree*.
Both volumes and their snapshots get added as standalone disks to the LXD host.

(storage-powerflex-volume-names)=
### Volume names

Due to a [limitation](storage-powerflex-limitations) in PowerFlex, volume names cannot exceed 31 characters.
Therefore the driver is using the volume's {config:option}`storage-powerflex-volume-conf:volatile.uuid` to generate a fixed length volume name.
A UUID of `5a2504b0-6a6c-4849-8ee7-ddb0b674fd14` will render to the base64-encoded string `WiUEsGpsSEmO592wtnT9FA==`.

To be able to identify the volume types and snapshots, special identifiers are prepended to the volume names:

Type            | Identifier   | Example
:--             | :---         | :----------
Container       | `c_`         | `c_WiUEsGpsSEmO592wtnT9FA==`
Virtual machine | `v_`         | `v_WiUEsGpsSEmO592wtnT9FA==.b`
Image (ISO)     | `i_`         | `i_WiUEsGpsSEmO592wtnT9FA==.i`
Custom volume   | `u_`         | `u_WiUEsGpsSEmO592wtnT9FA==`

(storage-powerflex-limitations)=
### Limitations

The `powerflex` driver has the following limitations:

Limit of snapshots in a single vTree
: An internal limitation in the PowerFlex vTree does not allow to take more than 126 snapshots of any volume in PowerFlex.
  This limit also applies to any child of any of the parent volume's snapshots.
  A single vTree can only have 126 branches.

Non-optimized image storage
: Due to the limit of 126 snapshots in the vTree, the PowerFlex driver doesn't come with support for optimized image storage.
  This would limit LXD to create only 126 instances from an image.
  Instead, when launching a new instance, the image's contents get copied to the instance's root volume.

Copying volumes
: PowerFlex does not support creating a copy of the volume so that it gets its own vTree.
  Therefore, LXD falls back to copying the volume on the local system.
  This implicates an increased use of bandwidth due to the volume's contents being transferred over the network twice.

Volume size constraints
: In PowerFlex, the size of a volume must be in multiples of 8 GiB.
  This results in the smallest possible volume size of 8 GiB.
  However, if not specified otherwise, volumes are getting thin-provisioned by LXD.
  PowerFlex volumes can only be increased in size.

Sharing custom volumes between instances
: The PowerFlex driver "simulates" volumes with content type `filesystem` by putting a file system on top of a PowerFlex volume.
  Therefore, custom storage volumes can only be assigned to a single instance at a time.

Sharing the PowerFlex storage pool between installations
: Sharing the same PowerFlex storage pool between multiple LXD installations is not supported.

Recovering PowerFlex storage pools
: Recovery of PowerFlex storage pools using `lxd recover` is not supported.

## Configuration options

The following configuration options are available for storage pools that use the `powerflex` driver and for storage volumes in these pools.

(storage-powerflex-pool-config)=
### Storage pool configuration

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group storage-powerflex-pool-conf start -->
    :end-before: <!-- config group storage-powerflex-pool-conf end -->
```

{{volume_configuration}}

(storage-powerflex-vol-config)=
### Storage volume configuration

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group storage-powerflex-volume-conf start -->
    :end-before: <!-- config group storage-powerflex-volume-conf end -->
```
