(instances-create)=
# How to create instances

To create an instance, you can use either the `lxc init` or the `lxc launch` command.
The `lxc init` command only creates the instance, while the `lxc launch` command creates and starts it.

## Usage

Enter the following command to create a container:

    lxc launch|init <image_server>:<image_name> <instance_name> [flags]

Image
: Images contain a basic operating system (for example, a Linux distribution) and some LXD-related information.
  Images for various operating systems are available on the built-in remote image servers.
  See {ref}`images` for more information.

  Unless the image is available locally, you must specify the name of the image server and the name of the image (for example, `images:ubuntu/22.04` for the 22.04 Ubuntu image from LXD's built-in image server).

Instance name
: Instance names must be unique within a LXD deployment (also within a cluster).
  See {ref}`instance-properties` for additional requirements.

Flags
: See `lxc launch --help` or `lxc init --help` for a full list of flags.
  The most common flags are:

  - `--config` to specify a configuration option for the new instance
  - `--device` to override {ref}`device options <devices>` for a device provided through a profile
  - `--profile` to specify a {ref}`profile <profiles>` to use for the new instance
  - `--network` or `--storage` to make the new instance use a specific network or storage pool
  - `--target` to create the instance on a specific cluster member
  - `--vm` to create a virtual machine instead of a container

## Pass a configuration file

Instead of specifying the instance configuration as flags, you can pass it to the command as a YAML file.

For example, to launch a container with the configuration from `config.yaml`, enter the following command:

    lxc launch images:ubuntu/22.04 ubuntu-config < config.yaml

```{tip}
Check the contents of an existing instance configuration (`lxc config show <instance_name> -e`) to see the required syntax of the YAML file.
```

## Examples

The following examples use `lxc launch`, but you can use `lxc init` in the same way.

### Launch a container

To launch a container with a Ubuntu 22.04 image from the `images` server using the instance name `ubuntu-container`, enter the following command:

    lxc launch images:ubuntu/22.04 ubuntu-container

### Launch a virtual machine

To launch a virtual machine with a Ubuntu 22.04 image from the `images` server using the instance name `ubuntu-vm`, enter the following command:

    lxc launch images:ubuntu/22.04 ubuntu-vm --vm

### Launch a container with specific configuration options

To launch a container and limit its resources to one vCPU and 192 MiB of RAM, enter the following command:

    lxc launch images:ubuntu/22.04 ubuntu-limited --config limits.cpu=1 --config limits.memory=192MiB

### Launch a VM on a specific cluster member

To launch a virtual machine on the cluster member `server2`, enter the following command:

    lxc launch images:ubuntu/22.04 ubuntu-container --vm --target server2

### Launch a container with a specific instance type

LXD supports simple instance types for clouds.
Those are represented as a string that can be passed at instance creation time.

The syntax allows the three following forms:

- `<instance type>`
- `<cloud>:<instance type>`
- `c<CPU>-m<RAM in GB>`

For example, the following three instance types are equivalent:

- `t2.micro`
- `aws:t2.micro`
- `c1-m1`

To launch a container with this instance type, enter the following command:

    lxc launch images:ubuntu/22.04 my-instance --type t2.micro

The list of supported clouds and instance types can be found at [`https://github.com/dustinkirkland/instance-type`](https://github.com/dustinkirkland/instance-type).
