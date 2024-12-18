(networks)=
# Networking setups

There are different ways to connect your instances to the Internet. The easiest method is to have LXD create a network bridge during initialization and use this bridge for all instances, but LXD supports many different and advanced setups for networking.

## Network devices

To grant direct network access to an instance, you must assign it at least one network device, also called {abbr}`NIC (Network Interface Controller)`.
You can configure the network device in one of the following ways:

- Use the default network bridge that you set up during the LXD initialization.
  Check the default profile to see the default configuration:

        lxc profile show default

  This method is used if you do not specify a network device for your instance.
- Use an existing network interface by adding it as a network device to your instance.
  This network interface is outside of LXD control.
  Therefore, you must specify all information that LXD needs to use the network interface.

  Use a command similar to the following:

        lxc config device add <instance_name> <device_name> nic nictype=<nic_type> ...

  See [Type: `nic`](devices-nic) for a list of available NIC types and their configuration properties.

  For example, you could add a pre-existing Linux bridge (`br0`) with the following command:

        lxc config device add <instance_name> eth0 nic nictype=bridged parent=br0
- {doc}`Create a managed network </howto/network_create>` and add it as a network device to your instance.
  With this method, LXD has all required information about the configured network, and you can directly attach it to your instance as a device:

        lxc network attach <network_name> <instance_name> <device_name>

  See {ref}`network-attach` for more information.

(managed-networks)=
## Managed networks

Managed networks in LXD are created and configured with the `lxc network [create|edit|set]` command.

Depending on the network type, LXD either fully controls the network or just manages an external network interface.

Note that not all {ref}`NIC types <devices-nic>` are supported as network types.
LXD can only set up some of the types as managed networks.

### Fully controlled networks

% Include content from [../reference/networks.md](../reference/networks.md)
```{include} ../reference/networks.md
    :start-after: <!-- Include start controlled intro -->
    :end-before: <!-- Include end controlled intro -->
```

{ref}`network-bridge`
: % Include content from [../reference/network_bridge.md](../reference/network_bridge.md)
  ```{include} ../reference/network_bridge.md
      :start-after: <!-- Include start bridge intro -->
      :end-before: <!-- Include end bridge intro -->
  ```

  In LXD context, the `bridge` network type creates an L2 bridge that connects the instances that use it together into a single network L2 segment.
  This makes it possible to pass traffic between the instances.
  The bridge can also provide local DHCP and DNS.

  This is the default network type.

{ref}`network-ovn`
: % Include content from [../reference/network_ovn.md](../reference/network_ovn.md)
  ```{include} ../reference/network_ovn.md
      :start-after: <!-- Include start OVN intro -->
      :end-before: <!-- Include end OVN intro -->
  ```

  In LXD context, the `ovn` network type creates a logical network.
  To set it up, you must install and configure the OVN tools.
  In addition, you must create an uplink network that provides the network connection for OVN.
  As the uplink network, you should use one of the external network types or a managed LXD bridge.

  ```{tip}
  Unlike the other network types, you can create and manage an OVN network inside a {ref}`project <projects>`.
  This means that you can create your own OVN network as a non-admin user, even in a restricted project.
  ```

### External networks

% Include content from [../reference/networks.md](../reference/network_external.md)
```{include} ../reference/networks.md
    :start-after: <!-- Include start external intro -->
    :end-before: <!-- Include end external intro -->
```

{ref}`network-macvlan`
: % Include content from [../reference/network_macvlan.md](../reference/network_macvlan.md)
  ```{include} ../reference/network_macvlan.md
      :start-after: <!-- Include start macvlan intro -->
      :end-before: <!-- Include end macvlan intro -->
  ```

  In LXD context, the `macvlan` network type provides a preset configuration to use when connecting instances to a parent macvlan interface.

{ref}`network-sriov`
: % Include content from [../reference/network_sriov.md](../reference/network_sriov.md)
  ```{include} ../reference/network_sriov.md
      :start-after: <!-- Include start SR-IOV intro -->
      :end-before: <!-- Include end SR-IOV intro -->
  ```

  In LXD context, the `sriov` network type provides a preset configuration to use when connecting instances to a parent SR-IOV interface.

{ref}`network-physical`
: % Include content from [../reference/network_physical.md](../reference/network_physical.md)
  ```{include} ../reference/network_physical.md
      :start-after: <!-- Include start physical intro -->
      :end-before: <!-- Include end physical intro -->
  ```

  It provides a preset configuration to use when connecting OVN networks to a parent interface.

## Recommendations

In general, if you can use a managed network, you should do so because networks are easy to configure and you can reuse the same network for several instances without repeating the configuration.

Which network type to choose depends on your specific use case.
If you choose a fully controlled network, it provides more functionality than using a network device.

As a general recommendation:

- If you are running LXD on a single system or in a public cloud, use a {ref}`network-bridge`, possibly in connection with the [Ubuntu Fan](https://www.youtube.com/watch?v=5cwd0vZJ5bw).
- If you are running LXD in your own private cloud, use an {ref}`network-ovn`.

  ```{note}
  OVN requires a shared L2 uplink network for proper operation.
  Therefore, using OVN is usually not possible if you run LXD in a public cloud.
  ```

- To connect an instance NIC to a managed network, use the `network` property rather than the `parent` property, if possible.
  This way, the NIC can inherit the settings from the network and you don't need to specify the `nictype`.

## Related topics

{{networks_how}}

{{networks_ref}}
