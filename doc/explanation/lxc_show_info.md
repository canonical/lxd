(lxc-show-info)=
# `lxc` `show` and `info`

For the entities managed by LXD, the `lxc` command provides a `list` sub-command, and might provide `show` and `info` sub-commands.
The purpose of the `info` sub-command is to show current state information, and the purpose of the `show` sub-command is to show configuration information and how the entity is used by other entities.

For example, the `lxc network info` command shows IP address and traffic statistics:

    Name: lxdbr0
    MAC address: 00:16:3e:d3:ec:41
    MTU: 1500
    State: up

    Ips:
      inet    192.0.2.1
      inet6   2001:db8:f4a1:53d2::1
      inet6   fe80::216:3eff:fed3:ec41

    Network usage:
      Bytes received: 127.66kB
      Bytes sent: 15.54kB
      Packets received: 1433
      Packets sent: 175

The `lxc network show` command, on the other hand, shows how the network is configured, and which entities are using the network:

    config:
      ipv4.address: 192.0.2.1/24
      ipv4.nat: "true"
      ipv6.address: 2001:db8:f4a1:53d2::1/64
      ipv6.nat: "true"
    description: ""
    name: lxdbr0
    type: bridge
    used_by:
    - /1.0/instances/ubuntu
    - /1.0/profiles/default
    managed: true
    status: Created
    locations:
    - none

Refer to the manual pages for details of the commands for managing entities:

- Instances: [`lxc list`](lxc_list.md), [`lxc info`](lxc_info.md)
- Images: [`lxc image list`](lxc_image_list.md), [`lxc image info`](lxc_image_info.md), [`lxc image show`](lxc_image_show.md)
- Networks: [`lxc network list`](lxc_network_list.md), [`lxc network info`](lxc_network_info.md), [`lxc network show`](lxc_network_show.md)
- Profiles: [`lxc profile list`](lxc_profile_list.md), [`lxc profile show`](lxc_profile_show.md)
- Projects: [`lxc project list`](lxc_project_list.md), [`lxc project info`](lxc_project_info.md), [`lxc project show`](lxc_project_show.md)
- Storage: [`lxc storage list`](lxc_storage_list.md), [`lxc storage info`](lxc_storage_info.md), [`lxc storage show`](lxc_storage_show.md)
- Cluster links: [`lxc cluster link list`](lxc_cluster_link_list.md), [`lxc cluster link info`](lxc_cluster_link_info.md), [`lxc cluster link show`](lxc_cluster_link_show.md)
