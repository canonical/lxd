(instances-create)=
# How to create instances

When creating an instance, you must specify the {ref}`image <about-images>` on which the instance should be based.

Images contain a basic operating system (for example, a Linux distribution) and some LXD-related information.
Images for various operating systems are available on the built-in remote image servers.
See {ref}`images` for more information.

If you don't specify a name for the instance, LXD will automatically generate one.
Instance names must be unique within a LXD deployment (also within a cluster).
See {ref}`instance-name-requirements` for additional requirements.

`````{tabs}
````{group-tab} CLI

To create an instance, you can use either the [`lxc init`](lxc_init.md) or the [`lxc launch`](lxc_launch.md) command.
The [`lxc init`](lxc_init.md) command only creates the instance, while the [`lxc launch`](lxc_launch.md) command creates and starts it.

Enter the following command to create a container:

    lxc launch|init <image_server>:<image_name> <instance_name> [flags]

Unless the image is available locally, you must specify the name of the image server and the name of the image (for example, `ubuntu:22.04` for the official 22.04 Ubuntu image).

See [`lxc launch --help`](lxc_launch.md) or [`lxc init --help`](lxc_init.md) for a full list of flags.
The most common flags are:

- `--config` to specify a configuration option for the new instance
- `--device` to override {ref}`device options <devices>` for a device provided through a profile, or to specify an {ref}`initial configuration for the root disk device <devices-disk-initial-config>` (syntax: `--device <device_name>,<device_option>=<value>`)
- `--profile` to specify a {ref}`profile <profiles>` to use for the new instance
- `--network` or `--storage` to make the new instance use a specific network or storage pool
- `--target` to create the instance on a specific cluster member
- `--vm` to create a virtual machine instead of a container

Instead of specifying the instance configuration as flags, you can pass it to the command as a YAML file.

For example, to launch a container with the configuration from `config.yaml`, enter the following command:

    lxc launch ubuntu:22.04 ubuntu-config < config.yaml

```{tip}
Check the contents of an existing instance configuration ([`lxc config show <instance_name> --expanded`](lxc_config_show.md)) to see the required syntax of the YAML file.
```
````

````{group-tab} API
To create an instance, send a POST request to the `/1.0/instances` endpoint:

    lxc query --request POST /1.0/instances --data '{
      "name": "<instance_name>",
      "source": {
        "alias": "<image_alias>",
        "protocol": "simplestreams",
        "server": "<server_URL>",
        "type": "image"
      }
    }'

The return value of this query contains an operation ID, which you can use to query the status of the operation:

    lxc query --request GET /1.0/operations/<operation_ID>

Use the following query to monitor the state of the instance:

    lxc query --request GET /1.0/instances/<instance_name>/state

See [`POST /1.0/instances`](swagger:/instances/instances_post) and [`GET /1.0/instances/{name}/state`](swagger:/instances/instance_state_get) for more information.

The request creates the instance, but does not start it.
To start an instance, send a PUT request to change the instance state:

    lxc query --request PUT /1.0/instances/<instance_name>/state --data '{"action": "start"}'

See {ref}`instances-manage-start` for more information.

````
`````

## Examples

The following examples create the instances, but don't start them.
If you are using the CLI client, you can use [`lxc launch`](lxc_launch.md) instead of [`lxc init`](lxc_init.md) to automatically start them after creation.

### Create a container

To create a container with an Ubuntu 22.04 image from the `ubuntu` server using the instance name `ubuntu-container`, enter the following command:

````{tabs}
```{group-tab} CLI
    lxc init ubuntu:22.04 ubuntu-container
```
```{group-tab} API
    lxc query --request POST /1.0/instances --data '{
      "name": "ubuntu-container",
      "source": {
        "alias": "22.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases",
        "type": "image"
      }
    }'
```
````

### Create a virtual machine

To create a virtual machine with an Ubuntu 22.04 image from the `ubuntu` server using the instance name `ubuntu-vm`, enter the following command:

````{tabs}
```{group-tab} CLI
    lxc init ubuntu:22.04 ubuntu-vm --vm
```
```{group-tab} API
    lxc query --request POST /1.0/instances --data '{
      "name": "ubuntu-vm",
      "source": {
        "alias": "22.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases",
        "type": "image"
      },
      "type": "virtual-machine"
    }'
```
````

Or with a bigger disk:

````{tabs}
```{group-tab} CLI
    lxc init ubuntu:22.04 ubuntu-vm-big --vm --device root,size=30GiB
```
```{group-tab} API
    lxc query --request POST /1.0/instances --data '{
      "devices": {
        "root": {
          "path": "/",
          "pool": "default",
          "size": "30GiB",
          "type": "disk"
        }
      },
      "name": "ubuntu-vm-big",
      "source": {
        "alias": "22.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases",
        "type": "image"
      },
      "type": "virtual-machine"
    }'
```
````

### Create a container with specific configuration options

To create a container and limit its resources to one vCPU and 192 MiB of RAM, enter the following command:

````{tabs}
```{group-tab} CLI
    lxc init ubuntu:22.04 ubuntu-limited --config limits.cpu=1 --config limits.memory=192MiB
```
```{group-tab} API
    lxc query --request POST /1.0/instances --data '{
      "config": {
        "limits.cpu": "1",
        "limits.memory": "192MiB"
      },
      "name": "ubuntu-limited",
      "source": {
        "alias": "22.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases",
        "type": "image"
      }
    }'
```
````

### Create a VM on a specific cluster member

To create a virtual machine on the cluster member `server2`, enter the following command:

````{tabs}
```{group-tab} CLI
    lxc init ubuntu:22.04 ubuntu-vm-server2 --vm --target server2
```
```{group-tab} API
    lxc query --request POST /1.0/instances?target=server2 --data '{
      "name": "ubuntu-vm-server2",
      "source": {
        "alias": "22.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases",
        "type": "image"
      },
      "type": "virtual-machine"
    }'
```
````

### Create a container with a specific instance type

LXD supports simple instance types for clouds.
Those are represented as a string that can be passed at instance creation time.

The syntax allows the three following forms:

- `<instance type>`
- `<cloud>:<instance type>`
- `c<CPU>-m<RAM in GiB>`

For example, the following three instance types are equivalent:

- `t2.micro`
- `aws:t2.micro`
- `c1-m1`

To create a container with this instance type, enter the following command:

````{tabs}
```{group-tab} CLI
    lxc init ubuntu:22.04 my-instance --type t2.micro
```
```{group-tab} API
    lxc query --request POST /1.0/instances --data '{
      "instance_type": "t2.micro",
      "name": "my-instance",
      "source": {
        "alias": "22.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases",
        "type": "image"
      }
    }'
```
````

The list of supported clouds and instance types can be found here:

- [Amazon Web Services](https://raw.githubusercontent.com/canonical/lxd/main/meta/instance-types/aws.yaml)
- [Google Compute Engine](https://raw.githubusercontent.com/canonical/lxd/main/meta/instance-types/gce.yaml)
- [Microsoft Azure](https://raw.githubusercontent.com/canonical/lxd/main/meta/instance-types/azure.yaml)

### Create a VM that boots from an ISO

To create a VM that boots from an ISO, you must first create a VM.
Let's assume that we want to create a VM and install it from the ISO image.
In this scenario, use the following command to create an empty VM:

````{tabs}
```{group-tab} CLI
    lxc init iso-vm --empty --vm
```
```{group-tab} API
    lxc query --request POST /1.0/instances --data '{
      "name": "iso-vm",
      "source": {
        "type": "none"
      },
      "type": "virtual-machine"
    }'
```
````

The second step is to import an ISO image that can later be attached to the VM as a storage volume:

`````{tabs}
````{group-tab} CLI
    lxc storage volume import <pool> <path-to-image.iso> iso-volume --type=iso
````
````{group-tab} API
    curl -X POST -H "Content-Type: application/octet-stream" -H "X-LXD-name: iso-volume" \
    -H "X-LXD-type: iso" --data-binary @<path-to-image.iso> --unix-socket /var/snap/lxd/common/lxd/unix.socket \
    lxd/1.0/storage-pools/<pool>/volumes/custom

```{note}
When importing an ISO image, you must send both binary data from a file and additional headers.
The [`lxc query`](lxc_query.md) command cannot do this, so you need to use `curl` or another tool instead.
```
````
`````

Lastly, attach the custom ISO volume to the VM using the following command:

````{tabs}
```{group-tab} CLI
    lxc config device add iso-vm iso-volume disk pool=<pool> source=iso-volume boot.priority=10
```
```{group-tab} API
    lxc query --request PATCH /1.0/instances/iso-vm --data '{
      "devices": {
        "iso-volume": {
          "boot.priority": "10",
          "pool": "<pool>",
          "source": "iso-volume",
          "type": "disk"
        }
      }
    }'
```
````

The {config:option}`device-disk-device-conf:boot.priority` configuration key ensures that the VM will boot from the ISO first.
Start the VM and {ref}`connect to the console <instances-console>` as there might be a menu you need to interact with:

````{tabs}
```{group-tab} CLI
    lxc start iso-vm --console
```
```{group-tab} API
    lxc query --request PUT /1.0/instances/iso-vm/state --data '{"action": "start"}'
    lxc query --request POST /1.0/instances/iso-vm/console --data '{
      "height": 24,
      "type": "console",
      "width": 80
    }'
```
````

Once you're done in the serial console, disconnect from the console using {kbd}`Ctrl`+{kbd}`a` {kbd}`q` and {ref}`connect to the VGA console <instances-console>` using the following command:

````{tabs}
```{group-tab} CLI
    lxc console iso-vm --type=vga
```
```{group-tab} API
    lxc query --request POST /1.0/instances/iso-vm/console --data '{
      "height": 24,
      "type": "vga",
      "width": 80
    }'
```
````

You should now see the installer. After the installation is done, detach the custom ISO volume:

`````{tabs}
````{group-tab} CLI
    lxc storage volume detach <pool> iso-volume iso-vm
````
````{group-tab} API
    lxc query --request GET /1.0/instances/iso-vm
    lxc query --request PUT /1.0/instances/iso-vm --data '{
      [...]
      "devices": {}
      [...]
    }'

```{note}
You cannot remove the device through a PATCH request, but you must use a PUT request.
Therefore, get the current configuration first and then provide the relevant configuration with an empty devices list through the PUT request.
```
````
`````

Now the VM can be rebooted, and it will boot from disk.
