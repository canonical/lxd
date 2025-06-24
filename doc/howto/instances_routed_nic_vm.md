(instances-routed-nic-vm)=
# How to add a routed NIC device to a virtual machine

When adding a {ref}`routed NIC device <nic-routed>` to an instance, you must configure the instance to use the link-local gateway IPs as default routes.
For containers, this is configured for you automatically.
For virtual machines, the gateways must be configured manually or via a mechanism like `cloud-init`.

To configure the gateways with `cloud-init`, firstly initialize an instance:

`````{tabs}
```{group-tab} CLI
    lxc init ubuntu:24.04 my-vm --vm
```
```{group-tab} API
    lxc query --request POST /1.0/instances --data '{
      "name": "my-vm",
      "source": {
        "alias": "24.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases/",
        "type": "image"
      },
      "type": "virtual-machine"
    }'
```
````{group-tab} UI
```{figure} /images/UI/routed_nic_create_instance.png
:width: 80%
:alt: Create an Ubuntu 24.04 LTS VM
```
````
`````

Then add the routed NIC device:

````{tabs}
```{group-tab} CLI
    lxc config device add my-vm eth0 nic nictype=routed parent=my-parent ipv4.address=192.0.2.2 ipv6.address=2001:db8::2
```
```{group-tab} API
    lxc query --request PATCH /1.0/instances/my-vm --data '{
      "devices": {
        "eth0": {
          "ipv4.address": "192.0.2.2",
          "ipv6.address": "2001:db8::2",
          "nictype": "routed",
          "parent": "my-parent",
          "type": "nic"
        }
      }
    }'
```
```{group-tab} UI
You cannot add a routed NIC device through the UI directly.
Therefore, go to the instance detail page, switch to the {guilabel}`Configuration` tab and select {guilabel}`YAML configuration`.
Then click {guilabel}`Edit instance` and add the routed NIC device to the `devices` section.
For example:

    devices:
      eth0:
        ipv4.address: 192.0.2.2
        ipv6.address: 2001:db8::2
        nictype: routed
        parent: my-parent
        type: nic
```
````

In this configuration, `my-parent` is your parent network, and the IPv4 and IPv6 addresses are within the subnet of the parent.

Next we will add some `netplan` configuration to the instance using the `cloud-init.network-config` configuration key:

````{tabs}
```{group-tab} CLI
    cat <<EOF | lxc config set my-vm cloud-init.network-config -
    network:
      version: 2
      ethernets:
        enp5s0:
          routes:
          - to: default
            via: 169.254.0.1
            on-link: true
          - to: default
            via: fe80::1
            on-link: true
          addresses:
          - 192.0.2.2/32
          - 2001:db8::2/128
    EOF
```
```{group-tab} API
    cat > cloud-init.txt <<EOF
    network:
      version: 2
      ethernets:
        enp5s0:
          routes:
          - to: default
            via: 169.254.0.1
            on-link: true
          - to: default
            via: fe80::1
            on-link: true
          addresses:
          - 192.0.2.2/32
          - 2001:db8::2/128
    EOF

    lxc query --request PATCH /1.0/instances/my-vm --data '{
      "config": {
        "cloud-init.network-config": "'"$(awk -v ORS='\\n' '1' cloud-init.txt)"'"
      }
    }'
```
```{group-tab} UI
On the instance detail page, switch to the {guilabel}`Advanced` > {guilabel}`Cloud-init` tab and click {guilabel}`Edit instance`.

Click the {guilabel}`Create override` icon for the {guilabel}`Network config` and enter the following configuration:

    network:
      version: 2
      ethernets:
        enp5s0:
          routes:
          - to: default
            via: 169.254.0.1
            on-link: true
          - to: default
            via: fe80::1
            on-link: true
          addresses:
          - 192.0.2.2/32
          - 2001:db8::2/128
```
````

This `netplan` configuration adds the {ref}`static link-local next-hop addresses <nic-routed>` (`169.254.0.1` and `fe80::1`) that are required.
For each of these routes we set `on-link` to `true`, which specifies that the route is directly connected to the interface.
We also add the addresses that we configured in our routed NIC device.
For more information on `netplan`, see [their documentation](https://netplan.readthedocs.io/en/latest/).

```{note}
This `netplan` configuration does not include a name server.
To enable DNS within the instance, you must set a valid DNS IP address.
If there is a `lxdbr0` network on the host, the name server can be set to that IP instead.
```

Before you start your instance, make sure that you have {ref}`configured the parent network <nic-routed-parent>` to enable proxy ARP/NDP.

Then start your instance:

````{tabs}
```{group-tab} CLI
    lxc start my-vm
```
```{group-tab} API
    lxc query --request PUT /1.0/instances/my-vm/state --data '{"action": "start"}'
```
```{group-tab} UI
Go to the instance list or the respective instance and click the {guilabel}`Start` button (▷).
```
````
