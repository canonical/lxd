# Network Forward configuration

Network forwards allow an external IP address (or specific ports on it) to be forwarded to an internal IP address
(or specific ports on it) in the network that the forward belongs to.

Each forward requires a single external listen address, combined with an optional default target address
(which causes any traffic not matched by a port specification to be forwarded to it) and an optional set of port
specifications (that allow specific port(s) on the listen address to be forwarded to specific port(s) on a target
address that is different than the default target address).

All target addresses must be within the same subnet as the network that the forward is associated to.

The default target address is specified in the forward's `config` set using the `target_address` field.

The listen addresses allowed vary depending on which [network type](#network-types) the forward is associated to.

## Properties
The following are network forward properties:

Property         | Type       | Required | Description
:--              | :--        | :--      | :--
listen\_address  | string     | yes      | IP address to listen on
description      | string     | no       | Description of Network Forward
config           | string set | no       | Config key/value pairs (Only `target_address` and `user.*` custom keys supported)
ports            | port list  | no       | Network forward port list

Network forward ports have the following properties:

Property          | Type       | Required | Description
:--               | :--        | :--      | :--
protocol          | string     | yes      | Protocol for port (`tcp` or `udp`)
listen\_port      | string     | yes      | Listen port(s) (e.g. `80,90-100`)
target\_address   | string     | yes      | IP address to forward to
target\_port      | string     | no       | Target port(s) (e.g. `70,80-90` or `90`), same as `listen_port` if empty
description       | string     | no       | Description of port(s)

## Network types

The following network types support forwards. See each network type section for more details.

 - [bridge](#network-bridge)
 - [ovn](#network-ovn)


### network: bridge

Any non-conflicting listen address is allowed.

The listen address used cannot overlap with a subnet that is in use with another network.

### network: ovn

The allowed listen addresses are those that are defined in the uplink network's `ipv{n}.routes` settings, and the
project's `restricted.networks.subnets` setting (if set).

The listen address used cannot overlap with a subnet that is in use with another network.
