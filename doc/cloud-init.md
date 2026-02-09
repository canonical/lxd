---
discourse: lxc:[First&#32;class&#32;cloud-init&#32;support](12559)
relatedlinks: "[Cloud-init&#32;documentation](https://cloudinit.readthedocs.org/)"
---

(cloud-init)=
# How to use `cloud-init`

```{youtube} https://www.youtube.com/watch?v=8OCG15TAldI
:title: LXD instance configuration with cloud-init
```

[`cloud-init`](https://cloud-init.io/) is a tool for automatically initializing and customizing an instance of a Linux distribution.

By adding `cloud-init` configuration to your instance, you can instruct `cloud-init` to execute specific actions at the first start of an instance.
Possible actions include, for example:

* Updating and installing packages
* Applying certain configurations
* Adding users
* Enabling services
* Running commands or scripts
* Automatically growing the file system of a VM to the size (quota) of the disk

See the {ref}`cloud-init:index` for detailed information.

```{note}
The `cloud-init` actions are run only once on the first start of the instance.
Rebooting the instance does not re-trigger the actions.
```

(cloud-init-support)=
## `cloud-init` support in images

To use `cloud-init`, you must base your instance on an image that has `cloud-init` installed:

* All images from the `ubuntu` and `ubuntu-daily` {ref}`image servers <remote-image-servers>` have `cloud-init` support.
  However, images for Ubuntu releases prior to 20.04 LTS require special handling to integrate properly with `cloud-init`, so that `lxc exec` works correctly with virtual machines that use those images. Refer to [VM `cloud-init`](vm-cloud-init-config).
* Images from the [`images` remote](https://images.lxd.canonical.com/) have `cloud-init`-enabled variants, which are usually bigger in size than the default variant.
  The cloud variants use the `/cloud` suffix, for example, `images:alpine/edge/cloud`.

## Configuration options

LXD supports two different sets of configuration options for configuring `cloud-init`: `cloud-init.*` and `user.*`.
Which of these sets you must use depends on the `cloud-init` support in the image that you use.
As a rule of thumb, newer images support the `cloud-init.*` configuration options, while older images support `user.*`.
However, there might be exceptions to that rule.

The following configuration options are supported:

* `cloud-init.vendor-data` or `user.vendor-data` (see {ref}`cloud-init:vendor-data`)
* `cloud-init.user-data` or `user.user-data` (see {ref}`cloud-init:user_data_formats`)
* `cloud-init.network-config` or `user.network-config` (see {ref}`cloud-init:network_config`)

For more information about the configuration options, see the [`cloud-init` instance options](instance-options-cloud-init), and the documentation for the {ref}`LXD data source <cloud-init:datasource_lxd>` in the `cloud-init` documentation.

```{note}
Ubuntu 20.04 and earlier have recent versions of the `cloud-init` package but support for the modern `cloud-init.*` configuration options is disabled in those series. As such, when using such old instances, remember to use the `user.*` configuration options instead.
```

### Vendor data and user data

Both `vendor-data` and `user-data` are used to provide {ref}`cloud configuration data <cloud-init:user_data_formats>` to `cloud-init`.

The main idea is that `vendor-data` is used for the general default configuration, while `user-data` is used for instance-specific configuration.
This means that you should specify `vendor-data` in a profile and `user-data` in the instance configuration.
LXD does not enforce this method, but allows using both `vendor-data` and `user-data` in profiles and in the instance configuration.

If both `vendor-data` and `user-data` are supplied for an instance, `cloud-init` merges the two configurations.
However, if you use the same keys in both configurations, merging might not be possible.
In this case, configure how `cloud-init` should merge the provided data.
See {ref}`cloud-init:merging_user_data` for instructions.

## How to configure `cloud-init`

To configure `cloud-init` for an instance, add the corresponding configuration options to a {ref}`profile <profiles>` that the instance uses or directly to the {ref}`instance configuration <instances-configure>`.

When configuring `cloud-init` directly for an instance, keep in mind that `cloud-init` runs only on instance start.
This means any changes to `cloud-init` configuration only take effect after the next instance start. To ensure `cloud-init` configurations are applied on every boot, LXD changes the instance ID whenever relevant `cloud-init` configuration keys are modified. This triggers `cloud-init` to fetch and apply the updated data from LXD as if it were the instance's first boot. For more information, see the `cloud-init` docs regarding {ref}`cloud-init:first_boot_determination`.

To add your configuration:

````{tabs}
```{group-tab} CLI
Write the configuration to a file and pass that file to the `lxc config` command.
For example, create `cloud-init.yml` with the following content:

    #cloud-config
    package_upgrade: true
    packages:
      - package1
      - package2

Then run the following command:

    lxc config set <instance_name> cloud-init.user-data - < cloud-init.yml
```
```{group-tab} API
Provide the `cloud-init` configuration as a string with escaped newline characters.

For example:

    lxc query --request PATCH /1.0/instances/<instance_name> --data '{
      "config": {
        "cloud-init.user-data": "#cloud-config\npackage_upgrade: true\npackages:\n  - package1\n  - package2"
      }
    }'

Alternatively, to avoid mistakes, write the configuration to a file and include that in your request.
For example, create `cloud-init.txt` with the following content:

    #cloud-config
    package_upgrade: true
    packages:
      - package1
      - package2

Then send the following request:

    lxc query --request PATCH /1.0/instances/<instance_name> --data '{
    "config": {
      "cloud-init.user-data": "'"$(awk -v ORS='\\n' '1' cloud-init.txt)"'"
      }
    }'
```
```{group-tab} UI
Go to the {guilabel}`Configuration` tab of the instance detail page and select {guilabel}`Advanced > Cloud init`.
Then click {guilabel}`Edit instance` and override the configuration for one or more of the `cloud-init` configuration options.
```
````

### YAML format for `cloud-init` configuration

The `cloud-init` options require YAML's [literal style format](https://yaml.org/spec/1.2.2/#812-literal-style).
You use a pipe symbol (`|`) to indicate that all indented text after the pipe should be passed to `cloud-init` as a single string, with new lines and indentation preserved.

The `vendor-data` and `user-data` options usually start with `#cloud-config`. But `cloud-init` has an array of [configuration formats](https://docs.cloud-init.io/en/latest/explanation/format.html#configuration-types) available.

For example:

```yaml
config:
  cloud-init.user-data: |
    #cloud-config
    package_upgrade: true
    packages:
      - package1
      - package2
```

```yaml
config:
  cloud-init.user-data: |
    #!/usr/bin/bash
    echo hello | tee -a /tmp/example.txt
```

```{tip}
See {ref}`How to validate user data <cloud-init:check_user_data_cloud_config>` for information on how to check whether the syntax is correct.
```

## How to check the `cloud-init` status

`cloud-init` runs automatically on the first start of an instance.
Depending on the configured actions, it might take a while until it finishes.

To check the `cloud-init` status, log on to the instance and enter the following command:

    cloud-init status

If the result is `status: running`, `cloud-init` is still working. If the result is `status: done`, it has finished.

Alternatively, use the `--wait` flag to be notified only when `cloud-init` is finished:

```{terminal}
:user: root
:host: instance

cloud-init status --wait

.....................................
status: done
```

## How to specify user or vendor data

The `user-data` and `vendor-data` configuration can be used to, for example, upgrade or install packages, add users, or run commands.

The provided values must have a first line that indicates what type of {ref}`user data format <cloud-init:user_data_formats>` is being passed to `cloud-init`.
For activities like upgrading packages or setting up a user, `#cloud-config` is the data format to use.

The configuration data is stored in the following files in the instance's root file system:

* `/var/lib/cloud/instance/cloud-config.txt`
* `/var/lib/cloud/instance/user-data.txt`

### Examples

See the following sections for the user data (or vendor data) configuration for different example use cases.

You can find more advanced {ref}`examples <cloud-init:yaml_examples>` in the `cloud-init` documentation.

#### Upgrade packages

To trigger a package upgrade from the repositories for the instance right after the instance is created, use the `package_upgrade` key:

```yaml
config:
  cloud-init.user-data: |
    #cloud-config
    package_upgrade: true
```

#### Install packages

To install specific packages when the instance is set up, use the `packages` key and specify the package names as a list:

```yaml
config:
  cloud-init.user-data: |
    #cloud-config
    packages:
      - git
      - openssh-server
```

#### Set the time zone

To set the time zone for the instance on instance creation, use the `timezone` key:

```yaml
config:
  cloud-init.user-data: |
    #cloud-config
    timezone: Europe/Rome
```

#### Run commands

To run a command (such as writing a marker file), use the `runcmd` key and specify the commands as a list:

```yaml
config:
  cloud-init.user-data: |
    #cloud-config
    runcmd:
      - [touch, /run/cloud.init.ran]
```

#### Add a user account

To add a user account, use the `users` key.
See the {ref}`cloud-init:reference/examples:including users and groups` example in the `cloud-init` documentation for details about default users and which keys are supported.

```yaml
config:
  cloud-init.user-data: |
    #cloud-config
    users:
      - name: documentation_example
```

## How to specify network configuration data

By default, `cloud-init` configures a DHCP client on an instance's `eth0` interface.
You can define your own network configuration using the `network-config` option to override the default configuration (this is due to how the template is structured).

`cloud-init` then renders the relevant network configuration on the system using either `ifupdown` or `netplan`, depending on the Ubuntu release.

The configuration data is stored in the following files in the instance's root file system:

* `/var/lib/cloud/seed/nocloud-net/network-config`
* `/etc/network/interfaces.d/50-cloud-init.cfg` (if using `ifupdown`)
* `/etc/netplan/50-cloud-init.yaml` (if using `netplan`)

### Example

To configure a specific network interface with a static IPv4 address and also use a custom name server, use the following configuration:

```yaml
config:
  cloud-init.network-config: |
    version: 2
    ethernets:
      eth1:
        addresses:
          - 10.10.101.20/24
        gateway4: 10.10.101.1
        nameservers:
          addresses:
            - 10.10.10.254
```

## How to inject SSH keys into instances

To inject SSH keys into LXD instances for an arbitrary user, use the configuration key `cloud-init.ssh-keys.<keyName>`.

Use the format `<user>:<key>` for its value, where `<user>` is a Linux username and `<key>` can be either a pure SSH public key or an import ID for a key hosted elsewhere. For example, `root:gh:githubUser` and `myUser:ssh-keyAlg publicKeyHash` are valid values. To prevent a particular SSH key from being inherited from a profile by an instance, edit the instance configuration by setting the `cloud-init.ssh-keys.<keyName>` key that references the target SSH key to `none`, and the key will not be injected.

The contents of the `cloud-init.ssh-keys.<keyName>` keys are merged into both {config:option}`instance-cloud-init:cloud-init.vendor-data` and {config:option}`instance-cloud-init:cloud-init.user-data` before being passed to the guest, following the `cloud-config` specification. (See the {ref}`cloud-init documentation <cloud-init:user_data_formats>` for details.) Therefore, keys defined via `cloud-init.ssh-keys.<keyName>` cannot be applied if LXD cannot parse the existing `cloud-init.vendor-data` and `cloud-init.user-data` for that instance. This might occur if those keys are not in YAML format or contain invalid YAML. Other configuration formats are not yet supported.

You can define SSH keys via `cloud-init.vendor-data` or `cloud-init.user-data` directly. Keys defined using `cloud-init.ssh-keys.<keyName>` do not conflict with those defined in either of those settings. For details on defining SSH keys with `cloud-config`, see {ref}`the cloud-init documentation for SSH configuration <cloud-init:cce-ssh>`. Changing a `cloud-init.*` key does not remove previously applied keys.

Since `cloud-init` only runs on instance start, updates to `cloud-init.*` keys on a running instance only take effect after restart.

### Examples

The following command injects `someuser`'s key from Launchpad into the newly created `container`:

```bash
lxc launch ubuntu:24.04 container -c cloud-init.ssh-keys.mykey=root:lp:someuser
```

The example profile configuration below defines a key to be injected on an instance. The injected key enables the owner of the private key to SSH into the instance as a user named `user`:

```yaml
config:
  cloud-init.vendor-data: |
    users:
      - name: user
        ssh_authorized_keys: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJFDWcYmMrCZdk9JI29bAiHKD90oEUr8tqK5VvoO8Vcj
```
