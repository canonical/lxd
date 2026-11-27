(devices-nic)=
# Type: `nic`

```{youtube} https://www.youtube.com/watch?v=W62eno28KMY
   :title: LXD NIC devices
```

```{note}
The `nic` device type is supported for both containers and VMs.

NICs support hotplugging for both containers and VMs (with the exception of the `ipvlan` NIC type).
```

Network devices, also referred to as *Network Interface Controllers* or *NICs*, supply a connection to a network.
LXD supports several different types of network devices (*NIC types*).

## `nictype` vs. `network`

When adding a network device to an instance, there are two methods to specify the type of device that you want to add: through the `nictype` device option or the `network` device option.

These two device options are mutually exclusive, and you can specify only one of them when you create a device.
However, note that when you specify the `network` option, the `nictype` option is derived automatically from the network type.

`nictype`
: When using the `nictype` device option, you can specify a network interface that is not controlled by LXD.
  Therefore, you must specify all information that LXD needs to use the network interface.

  When using this method, the `nictype` option must be specified when creating the device, and it cannot be changed later.

`network`
: When using the `network` device option, the NIC is linked to an existing {ref}`managed network <managed-networks>`.
  In this case, LXD has all required information about the network, and you need to specify only the network name when adding the device.

  When using this method, LXD derives the `nictype` option automatically.
  The value is read-only and cannot be changed.

  Other device options that are inherited from the network are marked with a "yes" in the "Managed" field of the NIC-specific device options.
  You cannot customize these options directly for the NIC if you're using the `network` method.

See {ref}`networks` for more information.

## Available NIC types

The following NICs can be added using the `nictype` or `network` options:

- [`bridged`](nic-bridged): Uses an existing bridge on the host and creates a virtual device pair to connect the host bridge to the instance.
- [`macvlan`](nic-macvlan): Sets up a new network device based on an existing one, but using a different MAC address.
- [`sriov`](nic-sriov): Passes a virtual function of an SR-IOV-enabled physical network device into the instance.
- [`physical`](nic-physical): Passes a physical device from the host through to the instance.
  The targeted device will vanish from the host and appear in the instance.

The following NICs can be added using only the `network` option:

- [`ovn`](nic-ovn): Uses an existing OVN network and creates a virtual device pair to connect the instance to it.

The following NICs can be added using only the `nictype` option:

- [`ipvlan`](nic-ipvlan): Sets up a new network device based on an existing one, using the same MAC address but a different IP.
- [`p2p`](nic-p2p): Creates a virtual device pair, putting one side in the instance and leaving the other side on the host.
- [`routed`](nic-routed): Creates a virtual device pair to connect the host to the instance and sets up static routes and proxy ARP/NDP entries to allow the instance to join the network of a designated parent interface.

The available device options depend on the NIC type and are listed in the following sections.

(nic-bridged)=
### `nictype`: `bridged`

```{note}
You can select this NIC type through the `nictype` option or the `network` option (see {ref}`network-bridge` for information about the managed `bridge` network).
```

A `bridged` NIC uses an existing bridge on the host and creates a virtual device pair to connect the host bridge to the instance.

#### Device options

NIC devices of type `bridged` have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-nic-bridged-device-conf start -->
    :end-before: <!-- config group device-nic-bridged-device-conf end -->
```

#### Configuration examples

Add a `bridged` network device to an instance, connecting to a LXD managed network:

    lxc network create <network_name> --type=bridge
    lxc config device add <instance_name> <device_name> nic network=<network_name>

Note that `bridge` is the type when creating a managed bridge network, while the device `nictype` that is required when connecting to an unmanaged bridge is `bridged`.

Add a `bridged` network device to an instance, connecting to an existing bridge interface with `nictype`:

    lxc config device add <instance_name> <device_name> nic nictype=bridged parent=<existing_bridge>

See {ref}`network-create` and {ref}`instances-configure-devices` for more information.

(nic-macvlan)=
### `nictype`: `macvlan`

```{note}
You can select this NIC type through the `nictype` option or the `network` option (see {ref}`network-macvlan` for information about the managed `macvlan` network).
```

A `macvlan` NIC sets up a new network device based on an existing one, but using a different MAC address.

If you are using a `macvlan` NIC, communication between the LXD host and the instances is not possible.
Both the host and the instances can talk to the gateway, but they cannot communicate directly.

#### Device options

NIC devices of type `macvlan` have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-nic-macvlan-device-conf start -->
    :end-before: <!-- config group device-nic-macvlan-device-conf end -->
```

#### Configuration examples

Add a `macvlan` network device to an instance, connecting to a LXD managed network:

    lxc network create <network_name> --type=macvlan parent=<existing_NIC>
    lxc config device add <instance_name> <device_name> nic network=<network_name>

Add a `macvlan` network device to an instance, connecting to an existing network interface with `nictype`:

    lxc config device add <instance_name> <device_name> nic nictype=macvlan parent=<existing_NIC>

See {ref}`network-create` and {ref}`instances-configure-devices` for more information.

(nic-sriov)=
### `nictype`: `sriov`

```{note}
You can select this NIC type through the `nictype` option or the `network` option (see {ref}`network-sriov` for information about the managed `sriov` network).
```

An `sriov` NIC passes a virtual function of an SR-IOV-enabled physical network device into the instance.

An SR-IOV-enabled network device associates a set of virtual functions (VFs) with the single physical function (PF) of the network device.
PFs are standard PCIe functions.
VFs, on the other hand, are very lightweight PCIe functions that are optimized for data movement.
They come with a limited set of configuration capabilities to prevent changing properties of the PF.

Given that VFs appear as regular PCIe devices to the system, they can be passed to instances just like a regular physical device.

VF allocation
: The `sriov` interface type expects to be passed the name of an SR-IOV enabled network device on the system via the `parent` property.
  LXD then checks for any available VFs on the system.

  By default, LXD allocates the first free VF it finds.
  If it detects that either none are enabled or all currently enabled VFs are in use, it bumps the number of supported VFs to the maximum value and uses the first free VF.
  If all possible VFs are in use or the kernel or card doesn't support incrementing the number of VFs, LXD returns an error.

  ```{note}
  If you need LXD to use a specific VF, use a `physical` NIC instead of a `sriov` NIC and set its `parent` option to the VF name.
  ```

#### Device options

NIC devices of type `sriov` have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-nic-sriov-device-conf start -->
    :end-before: <!-- config group device-nic-sriov-device-conf end -->
```

#### Configuration examples

Add a `sriov` network device to an instance, connecting to a LXD managed network:

    lxc network create <network_name> --type=sriov parent=<sriov_enabled_NIC>
    lxc config device add <instance_name> <device_name> nic network=<network_name>

Add a `sriov` network device to an instance, connecting to an existing SR-IOV-enabled interface with `nictype`:

    lxc config device add <instance_name> <device_name> nic nictype=sriov parent=<sriov_enabled_NIC>

See {ref}`network-create` and {ref}`instances-configure-devices` for more information.

(nic-physical)=
### `nictype`: `physical`

```{note}
- You can select this NIC type through the `nictype` option or the `network` option (see {ref}`network-physical` for information about the managed `physical` network).
- You can have only one `physical` NIC for each parent device.
```

A `physical` NIC provides straight physical device pass-through from the host.
The targeted device will vanish from the host and appear in the instance (which means that you can have only one `physical` NIC for each targeted device).

#### Device options

NIC devices of type `physical` have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-nic-physical-device-conf start -->
    :end-before: <!-- config group device-nic-physical-device-conf end -->
```

#### Configuration examples

Add a `physical` network device to an instance, connecting to an existing physical network interface with `nictype`:

    lxc config device add <instance_name> <device_name> nic nictype=physical parent=<physical_NIC>

Adding a `physical` network device to an instance using a managed network is not possible, because the `physical` managed network type is intended to be used only with OVN networks.

See {ref}`instances-configure-devices` for more information.

(nic-ovn)=
### `nictype`: `ovn`

```{note}
You can select this NIC type only through the `network` option (see {ref}`network-ovn` for information about the managed `ovn` network).
```

An `ovn` NIC uses an existing OVN network and creates a virtual device pair to connect the instance to it.

(devices-nic-hw-acceleration)=
SR-IOV hardware acceleration
: To use `acceleration=sriov`, you must have a compatible SR-IOV physical NIC that supports the Ethernet switch device driver model (`switchdev`) in your LXD host.
  LXD assumes that the physical NIC (PF) is configured in `switchdev` mode and connected to the OVN integration OVS bridge, and that it has one or more virtual functions (VFs) active.

  To achieve this, follow these basic prerequisite setup steps:

   1. Set up PF and VF:

      1. Activate some VFs on PF (called `enp9s0f0np0` in the following example, with a PCI address of `0000:09:00.0`) and unbind them.
      1. Enable `switchdev` mode and `hw-tc-offload` on the PF.
      1. Rebind the VFs.

      ```
      echo 4 > /sys/bus/pci/devices/0000:09:00.0/sriov_numvfs
      for i in $(lspci -nnn | grep "Virtual Function" | cut -d' ' -f1); do echo 0000:$i > /sys/bus/pci/drivers/mlx5_core/unbind; done
      devlink dev eswitch set pci/0000:09:00.0 mode switchdev
      ethtool -K enp9s0f0np0 hw-tc-offload on
      for i in $(lspci -nnn | grep "Virtual Function" | cut -d' ' -f1); do echo 0000:$i > /sys/bus/pci/drivers/mlx5_core/bind; done
      ```

   1. Set up OVS by enabling hardware offload and adding the PF NIC to the integration bridge (normally called `br-int`):

      ```
      ovs-vsctl set open_vswitch . other_config:hw-offload=true
      systemctl restart openvswitch-switch
      ovs-vsctl add-port br-int enp9s0f0np0
      ip link set enp9s0f0np0 up
      ```

VDPA hardware acceleration
: To use `acceleration=vdpa`, you must have a compatible VDPA physical NIC.
  The setup is the same as for SR-IOV hardware acceleration, except that you must also enable the `vhost_vdpa` module and check that you have some available VDPA management devices :

  ```
  modprobe vhost_vdpa && vdpa mgmtdev show
  ```

#### Device options

NIC devices of type `ovn` have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-nic-ovn-device-conf start -->
    :end-before: <!-- config group device-nic-ovn-device-conf end -->
```

#### Configuration examples

An `ovn` network device must be added using a managed network.
To do so:

    lxc network create <network_name> --type=ovn network=<parent_network>
    lxc config device add <instance_name> <device_name> nic network=<network_name>

See {ref}`network-ovn-setup` for full instructions, and {ref}`network-create` and {ref}`instances-configure-devices` for more information.

(nic-ipvlan)=
### `nictype`: `ipvlan`

```{note}
- This NIC type is available only for containers, not for virtual machines.
- You can select this NIC type only through the `nictype` option.
- This NIC type does not support hotplugging.
```

An `ipvlan` NIC sets up a new network device based on an existing one, using the same MAC address but a different IP.

If you are using an `ipvlan` NIC, communication between the LXD host and the instances is not possible.
Both the host and the instances can talk to the gateway, but they cannot communicate directly.

LXD currently supports IPVLAN in L2 and L3S mode.
In this mode, the gateway is automatically set by LXD, but the IP addresses must be manually specified using the `ipv4.address` and/or `ipv6.address` options before the container is started.

DNS
: The name servers must be configured inside the container, because they are not set automatically.
  To do this, set the following `sysctls`:

   - When using IPv4 addresses:

     ```
     net.ipv4.conf.<parent>.forwarding=1
     ```

   - When using IPv6 addresses:

     ```
     net.ipv6.conf.<parent>.forwarding=1
     net.ipv6.conf.<parent>.proxy_ndp=1
     ```

#### Device options

NIC devices of type `ipvlan` have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-nic-ipvlan-device-conf start -->
    :end-before: <!-- config group device-nic-ipvlan-device-conf end -->
```

#### Configuration examples

Add an `ipvlan` network device to an instance, connecting to an existing network interface with `nictype`:

    lxc stop <instance_name>
    lxc config device add <instance_name> <device_name> nic nictype=ipvlan parent=<existing_NIC>

Adding an `ipvlan` network device to an instance using a managed network is not possible.

See {ref}`instances-configure-devices` for more information.

(nic-p2p)=
### `nictype`: `p2p`

```{note}
You can select this NIC type only through the `nictype` option.
```

A `p2p` NIC creates a virtual device pair, putting one side in the instance and leaving the other side on the host.

A `p2p` NIC can also be peered with a `p2p` NIC on another instance. The two instances must be in the same project and on the same cluster member. The peers are connected by a private bridge that carries untagged traffic and 802.1Q VLANs, so instances can run multiple private VLANs across the link. The bridge has no host addresses, its host-side local port is not a member of any VLAN, and LXD installs a local-in drop rule on the bridge, so instance traffic is not delivered to the host network stack.

The host-side bridge name is derived only from a link ID that LXD generates randomly for each device pair, and each side's host port name is generated randomly by LXD. Instance, project and device names do not influence them, and the host-side configuration is fixed by LXD. LXD stores the link ID in `volatile.<device>.peer.link_id` and the port name in `volatile.<device>.host_name`. This means the feature can be used safely from restricted projects: restricted projects do not allow setting or changing volatile keys, and instance configuration cannot rename, reconfigure or delete a host interface. A peered `p2p` NIC is allowed even when {config:option}`project-restricted:restricted.devices.nic` is `managed`, because it does not attach to a host network; a `p2p` NIC that does not set `peer` is still refused. LXD also verifies an ownership marker on the bridge and ports before reusing or removing them.

#### Device options

NIC devices of type `p2p` have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-nic-p2p-device-conf start -->
    :end-before: <!-- config group device-nic-p2p-device-conf end -->
```

#### Configuration examples

Add a `p2p` network device to an instance using `nictype`:

    lxc config device add <instance_name> <device_name> nic nictype=p2p

Adding a `p2p` network device to an instance using a managed network is not possible.

##### Peered `p2p` NICs

To connect two instances, add a `p2p` NIC to each instance and set the `peer` option on both devices to the other instance and device:

    lxc config device add c1 p2p0 nic nictype=p2p name=eth1 peer=c2:p2p0
    lxc config device add c2 p2p0 nic nictype=p2p name=eth1 peer=c1:p2p0

The `peer` value uses the `<instance_name>:<device_name>` format. Both devices must reference each other. The peer instance must already exist when the first device is added. Either side can be added first; mutual validation is enforced when the instances start. Both instances must be in the same project and on the same cluster member. The instances can be containers, virtual machines, or one of each.

Multiple peer links can be configured between the same two instances; each device pair uses its own private bridge.

You can run the same two `lxc config device add` commands above while both instances are running. Add one device, then the other. The first interface appears in its instance immediately, but remains alone on its private bridge until the reciprocal device is added. If you stop the first instance before completing the pair, configure the reciprocal device before restarting it.

To disconnect the link while both instances are running, remove one endpoint with `lxc config device remove c1 p2p0`. The interface on `c2` stays in place. Add `p2p0` back on `c1` to reconnect, or remove `p2p0` on `c2` to finish removing the pair.

The link carries untagged traffic and tagged 802.1Q VLANs. VLAN 1 is used as the untagged (native) VLAN on the link: frames tagged with VLAN 1 are treated as untagged, so VLAN 1 cannot be used as a tagged VLAN. Use VLANs 2-4094 for tagged traffic. To use VLANs, create VLAN interfaces inside the instances in the usual way; no VLAN configuration is required on the device. The private bridge is retained while the pair exists. It is removed when the device is removed, or when the `peer` option is unset and the instance is next started or stopped.

The `peer` option cannot be combined with `host_name`, `mtu`, `queue.tx.length`, `ipv4.routes`, `ipv6.routes`, or `limits.*`. The host-side interfaces are configured entirely by LXD so that instance configuration cannot influence host networking. Ephemeral instances cannot be peered. To change the `peer` value on an existing NIC, stop both instances first, or remove the NIC and add a new one with the desired peer. Deleting an instance is allowed even while another instance still references it; the surviving device's peer reference is left dangling and must be repaired before that instance can start again. If the surviving instance is still running, its interface is left in place. Renaming an instance is blocked while it is peered.

See {ref}`instances-configure-devices` for more information.

(nic-routed)=
### `nictype`: `routed`

```{note}
You can select this NIC type only through the `nictype` option.
```

A `routed` NIC creates a virtual device pair to connect the host to the instance and sets up static routes and proxy ARP/NDP entries to allow the instance to join the network of a designated parent interface.
For containers it uses a virtual Ethernet device pair, and for VMs it uses a TAP device.

This NIC type is similar in operation to `ipvlan`, in that it allows an instance to join an external network without needing to configure a bridge and shares the host's MAC address.
However, it differs from `ipvlan` because it does not need IPVLAN support in the kernel, and the host and the instance can communicate with each other.

This NIC type respects `netfilter` rules on the host and uses the host's routing table to route packets, which can be useful if the host is connected to multiple networks.

IP addresses, gateways and routes
: You must manually specify the IP addresses (using `ipv4.address` and/or `ipv6.address`) before the instance is started.

  For containers, the NIC configures the following link-local gateway IPs on the host end and sets them as the default gateways in the container's NIC interface:

      169.254.0.1
      fe80::1

  For VMs, the gateways must be configured manually or via a mechanism like `cloud-init` (see the {ref}`how to guide <instances-routed-nic-vm>`).

  ```{note}
  If your container image is configured to perform DHCP on the interface, it will likely remove the automatically added configuration.
  In this case, you must configure the IP addresses and gateways manually or via a mechanism like `cloud-init`.
  ```

  The NIC type configures static routes on the host pointing to the instance's `veth` interface for all of the instance's IPs.

Multiple IP addresses
: Each NIC device can have multiple IP addresses added to it.

  However, it might be preferable to use multiple `routed` NIC interfaces instead.
  In this case, set the `ipv4.gateway` and `ipv6.gateway` values to `none` on any subsequent interfaces to avoid default gateway conflicts.
  Also consider specifying a different host-side address for these subsequent interfaces using `ipv4.host_address` and/or `ipv6.host_address`.

(nic-routed-parent)=
Parent interface
: This NIC can operate with and without a `parent` network interface set.

: With the `parent` network interface set, proxy ARP/NDP entries of the instance's IPs are added to the parent interface, which allows the instance to join the parent interface's network at layer 2.
: To enable this, the following network configuration must be applied on the host via `sysctl`:

   - When using IPv4 addresses:

     ```
     net.ipv4.conf.<parent>.forwarding=1
     ```

   - When using IPv6 addresses:

     ```
     net.ipv6.conf.all.forwarding=1
     net.ipv6.conf.<parent>.forwarding=1
     net.ipv6.conf.all.proxy_ndp=1
     net.ipv6.conf.<parent>.proxy_ndp=1
     ```

#### Device options

NIC devices of type `routed` have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-nic-routed-device-conf start -->
    :end-before: <!-- config group device-nic-routed-device-conf end -->
```

#### Configuration examples

Add a `routed` network device to an instance using `nictype`:

    lxc config device add <instance_name> <device_name> nic nictype=routed ipv4.address=192.0.2.2 ipv6.address=2001:db8::2

Adding a `routed` network device to an instance using a managed network is not possible.

See {ref}`instances-configure-devices` for more information.

## `bridged`, `macvlan` or `ipvlan` for connection to physical network

The `bridged`, `macvlan` and `ipvlan` interface types can be used to connect to an existing physical network.

`macvlan` effectively lets you fork your physical NIC, getting a second interface that is then used by the instance.
This method saves you from creating a bridge device and virtual Ethernet device pairs and usually offers better performance than a bridge.

The downside to this method is that `macvlan` devices, while able to communicate between themselves and to the outside, cannot talk to their parent device.
This means that you can't use `macvlan` if you ever need your instances to talk to the host itself.

In such case, a `bridge` device is preferable.
A bridge also lets you use MAC filtering and I/O limits, which cannot be applied to a `macvlan` device.

`ipvlan` is similar to `macvlan`, with the difference being that the forked device has IPs statically assigned to it and inherits the parent's MAC address on the network.
