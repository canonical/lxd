(import-machines-to-instances)=
# How to import physical or virtual machines to LXD instances

```{youtube} https://www.youtube.com/watch?v=F9GALjHtnUU
```

If you have an existing machine, either physical or virtual (VM or container), you can use the `lxd-convert` tool to create a LXD instance based on your existing disk or image.

The tool copies the provided partition, disk or image to the LXD storage pool of the provided LXD server, sets up an instance using that storage and allows you to configure additional settings for the new instance.

```{note}
If you want to configure your new instance during the conversion process, set up the entities that you want your instance to use before starting the conversion process.

By default, the new instance will use the entities specified in the `default` profile.
You can specify a different profile (or a profile list) to customize the configuration.
See {ref}`profiles` for more information.
You can also override {ref}`instance-options`, the {ref}`storage pool <storage-pools>` to be used and the size for the {ref}`storage volume <storage-volumes>`, and the {ref}`network <networking>` to be used.

Alternatively, you can update the instance configuration after the conversion is complete.
```

The tool can create both containers and virtual machines:

* When creating a container, you must provide a disk or partition that contains the root file system for the container.
  For example, this could be the `/` root disk of the machine or container where you are running the tool.
* When creating a virtual machine, you must provide a bootable disk, partition, or an image in raw, QCOW, QCOW2, VDI, VHDX, or VMDK format.
  This means that just providing a file system is not sufficient, and you cannot create a virtual machine from a container that you are running.
  It is also not possible to create a virtual machine from the physical machine that you are using to do the conversion, because the conversion tool would be using the disk that it is copying.
  Instead, you could provide a bootable image, or a bootable partition or disk that is currently not in use.

The tool can also inject the required VIRTIO drivers into the image:

* To convert the image into raw format and inject the VIRTIO drivers during the conversion, use the following command:

      lxd-convert --options=format,virtio

  ```{note}
  The conversion option `virtio` requires `virt-v2v-in-place` to be installed on the host where the LXD server runs.
  ```

* For converting Windows images from a foreign hypervisor (not from QEMU/KVM with Q35/`virtio-scsi`), you must install additional drivers on the host:
   * `/usr/share/virtio-win/virtio-win.iso`

     Download [`virtio-win.iso`](https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/stable-virtio/virtio-win.iso).
   * `/usr/share/virt-tools/rhsrvany.exe`
   * `/usr/share/virt-tools/pnp_wait.exe`

     `rhsrvany.exe` and `pnp_wait.exe` are provided in Ubuntu 24.04 and later in
     the [`rhsrvany`](https://launchpad.net/ubuntu/+source/rhsrvany) package.
     For other OS versions, download [`rhsrvany.exe` and `pnp_wait.exe`](https://github.com/rwmjones/rhsrvany?tab=readme-ov-file#binary-releases).

   ````{tip}
   The `lxd-convert` command with the `--options=format,virtio` option automatically converts the image and injects the VIRTIO drivers during the conversion.
   However, if you want to manually convert a Windows VM from a foreign hypervisor, you must install both the required Windows drivers (as described above) and `virt-v2v` (>= 2.3.4).

   <details>
   <summary>Expand to see how to convert your Windows VM using <code>virt-v2v</code></summary>

   Use `virt-v2v` to convert Windows image into `raw` format and include the required drivers.
   The resulting image is suitable for use with `lxd-convert`.

   ```
   # Example 1. Convert a VMDK image to a raw image
   sudo virt-v2v --block-driver virtio-scsi -o local -of raw -os ./os -i disk -if vmdk test-vm-disk.vmdk

   # Example 2. Convert a QEMU/KVM qcow2 image to a raw image
   sudo virt-v2v --block-driver virtio-scsi -o local -of raw -os ./os -i disk -if qcow2 test-vm-disk.qcow2

   # Example 3. Convert a VMX image to a raw image
   sudo virt-v2v --block-driver virtio-scsi -o local -of raw -os ./os -i vmx ./test-vm.vmx
   ```

   You can find the resulting image in the `os` directory and use it with `lxd-convert` on the next steps.
   In addition, when migrating already converted images, `lxd-convert` conversion options are not necessary.
   </details>
   ````

## Interactive instance import

Complete the following steps to convert an existing machine to a LXD instance:

1. Download the `bin.linux.lxd-convert` tool ([`bin.linux.lxd-convert.aarch64`](https://github.com/canonical/lxd/releases/latest/download/bin.linux.lxd-convert.aarch64) or [`bin.linux.lxd-convert.x86_64`](https://github.com/canonical/lxd/releases/latest/download/bin.linux.lxd-convert.x86_64)) from the **Assets** section of the latest [LXD release](https://github.com/canonical/lxd/releases).
1. Place the tool on the machine that you want to use to create the instance.
   Make it executable (usually by running `chmod u+x bin.linux.lxd-convert`).
1. Make sure that the machine has `rsync` and `file` installed.
   If they are missing, install them (for example, with `sudo apt install rsync file`).
1. Run the tool:

       sudo ./bin.linux.lxd-convert

   The tool then asks you to provide the information required for the conversion.

   1. Specify the LXD server URL, either as an IP address or as a DNS name.

      ```{note}
      The LXD server must be {ref}`exposed to the network <server-expose>`.
      If you want to import to a local LXD server, you must still expose it to the network.
      You can then specify `127.0.0.1` as the IP address to access the local server.
      ```

   1. Check and confirm the certificate fingerprint.
   1. Choose a method for authentication (see {ref}`authentication`).

      For example, if you choose using a certificate token, log on to the LXD server and create a token for the machine on which you are running the conversion tool with [`lxc config trust add`](lxc_config_trust_add.md).
      Then use the generated token to authenticate the tool.
   1. Choose whether to create a container or a virtual machine.
      See {ref}`containers-and-vms`.
   1. Specify a name for the instance that you are creating.
   1. Provide the path to a root file system (for containers) or a bootable disk, partition or image file (for virtual machines).
   1. For containers, optionally add additional file system mounts.
   1. For virtual machines, specify whether secure boot is supported.
   1. Optionally, configure the new instance.
      You can do so by specifying {ref}`profiles <profiles>`, directly setting {ref}`configuration options <instance-options>` or changing {ref}`storage <storage>` or {ref}`network <networking>` settings.

      Alternatively, you can configure the new instance after the conversion.
   1. When you are done with the configuration, start the conversion process.

   <details>
   <summary>Expand to see an example output for importing to a container</summary>

   ```{terminal}
   sudo ./bin.linux.lxd-convert

   Please provide LXD server URL: https://192.0.2.7:8443
   Certificate fingerprint: xxxxxxxxxxxxxxxxx
   ok (y/n)? y

   1) Use a certificate token
   2) Use an existing TLS authentication certificate
   3) Generate a temporary TLS authentication certificate
   Please pick an authentication mechanism above: 1
   Please provide the certificate token: xxxxxxxxxxxxxxxx

   Remote LXD server:
     Hostname: bar
     Version: 5.4

   Would you like to create a container (1) or virtual-machine (2)?: 1
   Name of the new instance: foo
   Please provide the path to a root filesystem: /
   Do you want to add additional filesystem mounts? [default=no]:

   Instance to be created:
     Name: foo
     Project: default
     Type: container
     Source: /

   Additional overrides can be applied at this stage:
   1) Begin the conversion with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 3
   Please specify config keys and values (key=value ...): limits.cpu=2

   Instance to be created:
     Name: foo
     Project: default
     Type: container
     Source: /
     Config:
       limits.cpu: "2"

   Additional overrides can be applied at this stage:
   1) Begin the conversion with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 4
   Please provide the storage pool to use: default
   Do you want to change the storage volume size? [default=no]: yes
   Please specify the storage volume size: 20GiB

   Instance to be created:
     Name: foo
     Project: default
     Type: container
     Source: /
     Storage pool: default
     Storage volume size: 20GiB
     Config:
       limits.cpu: "2"

   Additional overrides can be applied at this stage:
   1) Begin the conversion with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 5
   Please specify the network to use for the instance: lxdbr0

   Instance to be created:
     Name: foo
     Project: default
     Type: container
     Source: /
     Storage pool: default
     Storage volume size: 20GiB
     Network name: lxdbr0
     Config:
       limits.cpu: "2"

   Additional overrides can be applied at this stage:
   1) Begin the conversion with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 1
   Instance foo successfully created
   ```

   </details>
   <details>
   <summary>Expand to see an example output for importing to a VM</summary>

   ```{terminal}
   sudo ./bin.linux.lxd-convert

   Please provide LXD server URL: https://192.0.2.7:8443
   Certificate fingerprint: xxxxxxxxxxxxxxxxx
   ok (y/n)? y

   1) Use a certificate token
   2) Use an existing TLS authentication certificate
   3) Generate a temporary TLS authentication certificate
   Please pick an authentication mechanism above: 1
   Please provide the certificate token: xxxxxxxxxxxxxxxx

   Remote LXD server:
     Hostname: bar
     Version: 5.4

   Would you like to create a container (1) or virtual-machine (2)?: 2
   Name of the new instance: foo
   Please provide the path to a root filesystem: ./virtual-machine.img
   Does the VM support UEFI Secure Boot? [default=no]: no

   Instance to be created:
     Name: foo
     Project: default
     Type: virtual-machine
     Source: ./virtual-machine.img
     Config:
       security.secureboot: "false"

   Additional overrides can be applied at this stage:
   1) Begin the conversion with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 3
   Please specify config keys and values (key=value ...): limits.cpu=2

   Instance to be created:
     Name: foo
     Project: default
     Type: virtual-machine
     Source: ./virtual-machine.img
     Config:
       limits.cpu: "2"
       security.secureboot: "false"

   Additional overrides can be applied at this stage:
   1) Begin the conversion with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 4
   Please provide the storage pool to use: default
   Do you want to change the storage volume size? [default=no]: yes
   Please specify the storage volume size: 20GiB

   Instance to be created:
     Name: foo
     Project: default
     Type: virtual-machine
     Source: ./virtual-machine.img
     Storage pool: default
     Storage volume size: 20GiB
     Config:
       limits.cpu: "2"
       security.secureboot: "false"

   Additional overrides can be applied at this stage:
   1) Begin the conversion with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 5
   Please specify the network to use for the instance: lxdbr0

   Instance to be created:
     Name: foo
     Project: default
     Type: virtual-machine
     Source: ./virtual-machine.img
     Storage pool: default
     Storage volume size: 20GiB
     Network name: lxdbr0
     Config:
       limits.cpu: "2"
       security.secureboot: "false"

   Additional overrides can be applied at this stage:
   1) Begin the conversion with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 1
   Instance foo successfully created
   ```

   </details>
1. When the conversion is complete, check the new instance and update its configuration to the new environment.
   Typically, you must update at least the storage configuration (`/etc/fstab`) and the network configuration.

## Non-interactive instance import

Alternatively, the entire instance import configuration can be provided using `lxd-convert` flags.
If any required flag is missing, `lxd-convert` will interactively prompt for the missing value.
However, when the `--non-interactive` flag is used, an error is returned instead.

Note that if any flag contains an invalid value, an error is returned regardless of the mode (interactive or non-interactive).

The `lxd-convert` command supports the following flags that can be used in non-interactive conversion:

```
Instance configuration:
  -c, --config               Config key/value to apply to the new instance
      --mount-path           Additional container mount paths
      --name                 Name of the new instance
      --network              Network name
      --no-profiles          Create the instance with no profiles applied
      --profiles             Profiles to apply on the new instance (default [default])
      --project              Project name
      --source               Path to the root filesystem for containers, or to the block device or disk image file for virtual machines
      --storage              Storage pool name
      --storage-size         Size of the instance's storage volume
      --type                 Type of the instance to create (container or vm)

Target server:
      --server               Unix or HTTPS URL of the target server
      --token                Authentication token for HTTPS remote
      --cert-path            Trusted certificate path
      --key-path             Trusted certificate path

Other:
      --options strings      Comma-separated list of conversion options to apply. Allowed values are: [format, virtio] (default [format])
      --non-interactive      Prevent further interaction if conversion questions are incomplete
      --rsync-args           Extra arguments to pass to rsync
```

Example VM import to local LXD server:

```sh
lxd-convert \
  --name v1 \
  --type vm \
  --source "${sourcePath}" \
  --non-interactive
```

Example VM import to remote HTTPS server:

```sh
# Token from remote server.
token=$(lxc config trust add --name lxd-convert --quiet)

lxd-convert \
  --server https://example.com:8443 \
  --token "$token" \
  --name v1 \
  --type vm \
  --source "${sourcePath}" \
  --non-interactive
```

Example VM import with secure boot disabled and custom resource limits:

```sh
lxd-convert \
  --name v1 \
  --type vm \
  --source "${sourcePath}" \
  --config security.secureboot=false \
  --config limits.cpu=4 \
  --config limits.memory=4GiB \
  --non-interactive
```
