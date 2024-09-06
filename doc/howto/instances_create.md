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

Unless the image is available locally, you must specify the name of the image server and the name of the image (for example, `ubuntu:24.04` for the official Ubuntu 24.04 LTS image).

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

    lxc launch ubuntu:24.04 ubuntu-config < config.yaml

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

If you would like to start the instance upon creation, set the `start` property to true. The following example will create the container, then start it:

    lxc query --request POST /1.0/instances --data '{
      "name": "<instance_name>",
      "source": {
        "alias": "<image_alias>",
        "protocol": "simplestreams",
        "server": "<server_URL>",
        "type": "image"
      },
      "start": true
    }'

````

````{group-tab} UI

To create an instance, go to the {guilabel}`Instances` section and click {guilabel}`Create instance`.

On the resulting screen, optionally enter a name and description for the instance.
Then click {guilabel}`Browse images` to select the image to be used for the instance.
Depending on the selected image, you might be able to select the {ref}`instance type <expl-instances>` (container or virtual machine).
You can also specify one or more profiles to use for the instance.

To further tweak the instance configuration or add devices to the instance, go to any of the tabs under {guilabel}`Advanced`.
You can also edit the full instance configuration on the {guilabel}`YAML configuration` tab.

Finally, click {guilabel}`Create` or {guilabel}`Create and start` to create the instance.

````
`````

## Examples

The following CLI and API examples create the instances, but don't start them.
If you are using the CLI client, you can use [`lxc launch`](lxc_launch.md) instead of [`lxc init`](lxc_init.md) to automatically start them after creation.

In the UI, you can choose between {guilabel}`Create` and {guilabel}`Create and start` when you are ready to create the instance.

### Create a container

To create a container with an Ubuntu 24.04 LTS image from the `ubuntu` server using the instance name `ubuntu-container`:

`````{tabs}
```{group-tab} CLI
    lxc init ubuntu:24.04 ubuntu-container
```
```{group-tab} API
    lxc query --request POST /1.0/instances --data '{
      "name": "ubuntu-container",
      "source": {
        "alias": "24.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases",
        "type": "image"
      }
    }'
```
````{group-tab} UI
```{figure} /images/UI/create_instance_ex1.png
:width: 80%
:alt: Create an Ubuntu 24.04 LTS container
```
````
`````

### Create a virtual machine

To create a virtual machine with an Ubuntu 24.04 LTS image from the `ubuntu` server using the instance name `ubuntu-vm`:

`````{tabs}
```{group-tab} CLI
    lxc init ubuntu:24.04 ubuntu-vm --vm
```
```{group-tab} API
    lxc query --request POST /1.0/instances --data '{
      "name": "ubuntu-vm",
      "source": {
        "alias": "24.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases",
        "type": "image"
      },
      "type": "virtual-machine"
    }'
```
````{group-tab} UI
```{figure} /images/UI/create_instance_ex2.png
:width: 80%
:alt: Create an Ubuntu 24.04 LTS VM
```
````
`````

Or with a bigger disk:

`````{tabs}
```{group-tab} CLI
    lxc init ubuntu:24.04 ubuntu-vm-big --vm --device root,size=30GiB
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
        "alias": "24.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases",
        "type": "image"
      },
      "type": "virtual-machine"
    }'
```
````{group-tab} UI
```{figure} /images/UI/create_instance_ex2-2.png
:width: 80%
:alt: Configure the size of the root disk
```
````
`````

### Create a container with specific configuration options

To create a container and limit its resources to one vCPU and 8 GiB of RAM:

`````{tabs}
```{group-tab} CLI
    lxc init ubuntu:24.04 ubuntu-limited --config limits.cpu=1 --config limits.memory=8GiB
```
```{group-tab} API
    lxc query --request POST /1.0/instances --data '{
      "config": {
        "limits.cpu": "1",
        "limits.memory": "8GiB"
      },
      "name": "ubuntu-limited",
      "source": {
        "alias": "24.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases",
        "type": "image"
      }
    }'
```
````{group-tab} UI
```{figure} /images/UI/create_instance_ex3.png
:width: 80%
:alt: Configure resource limits
```
````
`````

### Create a VM on a specific cluster member

To create a virtual machine on the cluster member `micro2`, enter the following command:

`````{tabs}
```{group-tab} CLI
    lxc init ubuntu:24.04 ubuntu-vm-server2 --vm --target micro2
```
```{group-tab} API
    lxc query --request POST /1.0/instances?target=micro2 --data '{
      "name": "ubuntu-vm-server2",
      "source": {
        "alias": "24.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases",
        "type": "image"
      },
      "type": "virtual-machine"
    }'
```
````{group-tab} UI
```{figure} /images/UI/create_instance_ex4.png
:width: 80%
:alt: Specify which cluster member to create an instance on
```
````
`````

### Create a container with a specific instance type

LXD supports simple instance types for clouds.
Those are represented as a string that can be passed at instance creation time.

The list of supported clouds and instance types can be found at [`images.lxd.canonical.com/meta/instance-types/`](https://images.lxd.canonical.com/meta/instance-types/).

The syntax allows the three following forms:

- `<instance type>`
- `<cloud>:<instance type>`
- `c<CPU>-m<RAM in GiB>`

For example, the following three instance types are equivalent:

- `t2.micro`
- `aws:t2.micro`
- `c1-m1`

To create a container with this instance type:

````{tabs}
```{group-tab} CLI
    lxc init ubuntu:24.04 my-instance --type t2.micro
```
```{group-tab} API
    lxc query --request POST /1.0/instances --data '{
      "instance_type": "t2.micro",
      "name": "my-instance",
      "source": {
        "alias": "24.04",
        "protocol": "simplestreams",
        "server": "https://cloud-images.ubuntu.com/releases",
        "type": "image"
      }
    }'
```
```{group-tab} UI
Creating an instance with a specific cloud instance type is currently not possible through the UI.
Configure the corresponding options manually or through a profile.
```
````

(instances-create-iso)=
### Create a VM that boots from an ISO

To create a VM that boots from an ISO:

`````{tabs}
````{group-tab} CLI
<!-- iso_vm_step1 start -->
First, create an empty VM that we can later install from the ISO image:
<!-- iso_vm_step1 end -->

    lxc init iso-vm --empty --vm --config limits.cpu=2 --config limits.memory=4GiB --device root,size=30GiB

```{note}
Adapt the `limits.cpu`, `limits.memory`, and root size based on the hardware recommendations for the ISO image used.
```

<!-- iso_vm_step2 start -->
The second step is to import an ISO image that can later be attached to the VM as a storage volume:
<!-- iso_vm_step2 end -->

    lxc storage volume import <pool> <path-to-image.iso> iso-volume --type=iso

<!-- iso_vm_step3 start -->
Lastly, attach the custom ISO volume to the VM using the following command:
<!-- iso_vm_step3 end -->

    lxc config device add iso-vm iso-volume disk pool=<pool> source=iso-volume boot.priority=10

<!-- iso_vm_step4 start -->
The {config:option}`device-disk-device-conf:boot.priority` configuration key ensures that the VM will boot from the ISO first.
Start the VM and {ref}`connect to the console <instances-console>` as there might be a menu you need to interact with:
<!-- iso_vm_step4 end -->

    lxc start iso-vm --console

<!-- iso_vm_step5 start -->
Once you're done in the serial console, disconnect from the console using {kbd}`Ctrl`+{kbd}`a` {kbd}`q` and {ref}`connect to the VGA console <instances-console>` using the following command:
<!-- iso_vm_step5 end -->

    lxc console iso-vm --type=vga

<!-- iso_vm_step6 start -->
You should now see the installer. After the installation is done, detach the custom ISO volume:
<!-- iso_vm_step6 end -->

    lxc storage volume detach <pool> iso-volume iso-vm

<!-- iso_vm_step7 start -->
Now the VM can be rebooted, and it will boot from disk.
<!-- iso_vm_step7 end -->

```{note}
On Linux virtual machines, the {ref}`LXD agent can be manually installed <lxd-agent-manual-install>`.
```

````
````{group-tab} API
```{include} instances_create.md
       :start-after: <!-- iso_vm_step1 start -->
       :end-before: <!-- iso_vm_step1 end -->
```
    lxc query --request POST /1.0/instances --data '{
      "name": "iso-vm",
      "config": {
        "limits.cpu": "2",
        "limits.memory": "4GiB"
      },
      "devices": {
        "root": {
          "path": "/",
          "pool": "default",
          "size": "30GiB",
          "type": "disk"
        }
      },
      "source": {
        "type": "none"
      },
      "type": "virtual-machine"
    }'

```{note}
Adapt the values for `limits.cpu`, `limits.memory`, and `root: size` based on the hardware recommendations for the ISO image used.
```


```{include} instances_create.md
       :start-after: <!-- iso_vm_step2 start -->
       :end-before: <!-- iso_vm_step2 end -->
```
    curl -X POST -H "Content-Type: application/octet-stream" -H "X-LXD-name: iso-volume" \
    -H "X-LXD-type: iso" --data-binary @<path-to-image.iso> --unix-socket /var/snap/lxd/common/lxd/unix.socket \
    lxd/1.0/storage-pools/<pool>/volumes/custom

```{note}
When importing an ISO image, you must send both binary data from a file and additional headers.
The [`lxc query`](lxc_query.md) command cannot do this, so you need to use `curl` or another tool instead.
```

```{include} instances_create.md
       :start-after: <!-- iso_vm_step3 start -->
       :end-before: <!-- iso_vm_step3 end -->
```
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

```{include} instances_create.md
       :start-after: <!-- iso_vm_step4 start -->
       :end-before: <!-- iso_vm_step4 end -->
```
    lxc query --request PUT /1.0/instances/iso-vm/state --data '{"action": "start"}'
    lxc query --request POST /1.0/instances/iso-vm/console --data '{
      "height": 24,
      "type": "console",
      "width": 80
    }'

```{include} instances_create.md
       :start-after: <!-- iso_vm_step5 start -->
       :end-before: <!-- iso_vm_step5 end -->
```
    lxc query --request POST /1.0/instances/iso-vm/console --data '{
      "height": 24,
      "type": "vga",
      "width": 80
    }'

```{include} instances_create.md
       :start-after: <!-- iso_vm_step6 start -->
       :end-before: <!-- iso_vm_step6 end -->
```
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

```{include} instances_create.md
       :start-after: <!-- iso_vm_step7 start -->
       :end-before: <!-- iso_vm_step7 end -->
```

```{include} instances_create.md
       :start-after: <!-- iso_vm_step8 start -->
       :end-before: <!-- iso_vm_step8 end -->
```
````
````{group-tab} UI
In the {guilabel}`Create instance` dialog, click {guilabel}`Use custom ISO` instead of {guilabel}`Browse images`.
You can then upload your ISO file and install a VM from it.
````
`````

(lxd-agent-manual-install)=
### Install the LXD agent into virtual machine instances

In order for features like direct command execution (`lxc exec` & `lxc shell`), file transfers (`lxc file`) and detailed usage metrics (`lxc info`)
to work properly with virtual machines, an agent software is provided by LXD.

The virtual machine images from the official {ref}`remote image servers <remote-image-servers>` are pre-configured to load that agent on startup.

For other virtual machines, you may want to manually install the agent.

```{note}
The LXD agent is currently available only on Linux virtual machines using `systemd`.
```

LXD provides the agent through a remote `9p` file system and a `virtiofs` one that are both available under the mount name `config`.
To install the agent, you'll need to get access to the virtual machine and run the following commands as root:

    modprobe 9pnet_virtio
    mount -t 9p config /mnt -o access=0,transport=virtio || mount -t virtiofs config /mnt
    cd /mnt
    ./install.sh
    cd /
    umount /mnt
    reboot

You need to perform this task once.

### Create a Windows VM

To create a Windows VM, you must first prepare a Windows image.
See {ref}`images-repack-windows`.

The [How to install a Windows 11 VM using LXD](https://ubuntu.com/tutorials/how-to-install-a-windows-11-vm-using-lxd) tutorial shows how to prepare the image and create a Windows VM from it.
