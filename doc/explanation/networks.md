LXD supports the following network types:

 - [bridge](#network-bridge): Creates an L2 bridge for connecting instances to (can provide local DHCP and DNS). This is the default.
 - [macvlan](#network-macvlan): Provides preset configuration to use when connecting instances to a parent macvlan interface.
 - [sriov](#network-sriov): Provides preset configuration to use when connecting instances to a parent SR-IOV interface.
 - [ovn](#network-ovn): Creates a logical network using the OVN software defined networking system.
 - [physical](#network-physical): Provides preset configuration to use when connecting OVN networks to a parent interface.
