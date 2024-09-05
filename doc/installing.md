---
discourse: ubuntu:37327
---

(installing)=
# How to install LXD

The easiest way to install LXD is to {ref}`install one of the available packages <installing-from-package>`, but you can also {ref}`install LXD from the sources <installing-from-source>`.

After installing LXD, make sure you have a `lxd` group on your system.
Users in this group can interact with LXD.
See {ref}`installing-manage-access` for instructions.

(installing-release)=
## Choose your release

% Include content from [support.md](support.md)
```{include} support.md
    :start-after: <!-- Include start release -->
    :end-before: <!-- Include end release -->
```

LTS releases are recommended for production environments, because they benefit from regular bugfix and security updates.
However, there are no new features added to an LTS release, nor any kind of behavioral change.

To get all the latest features and monthly updates to LXD, use the feature release branch instead.

(installing-from-package)=
## Install LXD from a package

The LXD daemon only works on Linux.
The client tool ([`lxc`](lxc.md)) is available on most platforms.

### Linux

The easiest way to install LXD on Linux is to install the {ref}`installing-snap-package`, which is available for different Linux distributions.

If this option does not work for you, see the {ref}`installing-other`.

(installing-snap-package)=
#### Snap package

LXD publishes and tests [snap packages](https://snapcraft.io/lxd) that work for a number of Linux distributions (for example, Ubuntu, Arch Linux, Debian, Fedora, and OpenSUSE).

Complete the following steps to install the snap:

1. Check the [LXD snap page on Snapcraft](https://snapcraft.io/lxd) to see if a snap is available for your Linux distribution.
   If it is not, use one of the {ref}`installing-other`.

1. Install `snapd`.
   See the [installation instructions](https://snapcraft.io/docs/installing-snapd) in the Snapcraft documentation.

1. Install the snap package.
   For the latest feature release, use:

        sudo snap install lxd --channel=latest/stable

   For the LXD 5.21 LTS release, use:

        sudo snap install lxd --channel=5.21/stable

   For the LXD 5.0 LTS release, use:

        sudo snap install lxd --channel=5.0/stable

For more information about LXD snap packages (regarding more versions, update management etc.), see [Managing the LXD snap](https://discuss.linuxcontainers.org/t/managing-the-lxd-snap/8178).

```{note}
On Ubuntu 18.04 LTS, if you previously had the LXD deb package installed, you can migrate all your existing data over by installing the 5.0 snap and running the following commands:

        sudo install lxd --channel=5.0/stable
        sudo lxd.migrate

After successfully running the `lxd.migrate` command, you can then switch to a newer snap channel if desired, like the latest one:

        sudo refresh lxd --channel=latest/stable
```

If you want the current user to be able to interact with the LXD daemon, add it to the `lxd` group as the installation process does not add it for you:

```bash
getent group lxd | grep -qwF "$USER" || sudo usermod -aG lxd "$USER"
```

```{note}
{{must_start_new_session}}
```

(installing-other)=
#### Other installation options

Some Linux distributions provide installation options other than the snap package.

````{tabs}

```{group-tab} Alpine Linux

To install the feature branch of LXD on Alpine Linux, run:

    apk add lxd
```

```{group-tab} Arch Linux

To install the feature branch of LXD on Arch Linux, run:

    pacman -S lxd
```

```{group-tab} Fedora

Fedora RPM packages for LXC/LXD are available in the [COPR repository](https://copr.fedorainfracloud.org/coprs/ganto/lxc4/).

To install the LXD package for the feature branch, run:

    dnf copr enable ganto/lxc4
    dnf install lxd

See the [Installation Guide](https://github.com/ganto/copr-lxc4/wiki) for more detailed installation instructions.
```

```{group-tab} Gentoo

To install the feature branch of LXD on Gentoo, run:

    emerge --ask lxd
```

````

### Other operating systems

```{important}
The builds for other operating systems include only the client, not the server.
```

````{tabs}

```{group-tab} macOS

LXD publishes builds of the LXD client for macOS through [Homebrew](https://brew.sh/).

To install the feature branch of LXD, run:

    brew install lxc
```

```{group-tab} Windows

The LXD client on Windows is provided as a [Chocolatey](https://community.chocolatey.org/packages/lxc) package.
To install it:

1. Install Chocolatey by following the [installation instructions](https://docs.chocolatey.org/en-us/choco/setup).
1. Install the LXD client:

        choco install lxc
```

````

You can also find native builds of the LXD client on [GitHub](https://github.com/canonical/lxd/actions):

- LXD client for Linux: [`bin.linux.lxc.aarch64`](https://github.com/canonical/lxd/releases/latest/download/bin.linux.lxc.aarch64), [`bin.linux.lxc.x86_64`](https://github.com/canonical/lxd/releases/latest/download/bin.linux.lxc.x86_64)
- LXD client for Windows: [`bin.windows.lxc.aarch64.exe`](https://github.com/canonical/lxd/releases/latest/download/bin.windows.lxc.aarch64.exe), [`bin.windows.lxc.x86_64.exe`](https://github.com/canonical/lxd/releases/latest/download/bin.windows.lxc.x86_64.exe)
- LXD client for macOS: [`bin.macos.lxc.aarch64`](https://github.com/canonical/lxd/releases/latest/download/bin.macos.lxc.aarch64), [`bin.macos.lxc.x86_64`](https://github.com/canonical/lxd/releases/latest/download/bin.macos.lxc.x86_64)

To download a specific build:

1. Make sure that you are logged into your GitHub account.
1. Filter for the branch or tag that you are interested in (for example, the latest release tag or `main`).
1. Select the latest build and download the suitable artifact.

(installing-from-source)=
(installing_from_source)=
## Install LXD from source

Follow these instructions if you want to build and install LXD from the source code.

We recommend having the latest versions of `liblxc` (see {ref}`LXC requirements <requirements-lxc>`)
available for LXD development. Additionally, LXD requires a modern Golang (see {ref}`requirements-go`)
version to work. On Ubuntu, you can get those with:

```bash
sudo apt update
sudo apt install acl attr autoconf automake dnsmasq-base git libacl1-dev libcap-dev liblxc1 liblxc-dev libsqlite3-dev libtool libudev-dev liblz4-dev libuv1-dev make pkg-config rsync squashfs-tools tar tcl xz-utils ebtables
command -v snap >/dev/null || sudo apt-get install snapd
sudo snap install --classic go
```

```{note}
If you use the `liblxc-dev` package and get compile time errors when building the `go-lxc` module,
ensure that the value for `LXC_DEVEL` is `0` for your `liblxc` build. To check that, look at `/usr/include/lxc/version.h`.
If the `LXC_DEVEL` value is `1`, replace it with `0` to work around the problem. It's a packaging bug that is now fixed,
see [LP: #2039873](https://bugs.launchpad.net/ubuntu/+source/lxc/+bug/2039873).
```

There are a few storage drivers for LXD besides the default `dir` driver.
Installing these tools adds a bit to initramfs and may slow down your
host boot, but are needed if you'd like to use a particular driver:

```bash
sudo apt install lvm2 thin-provisioning-tools
sudo apt install btrfs-progs
```

To run the test suite, you'll also need:

```bash
sudo apt install busybox-static curl gettext jq sqlite3 socat bind9-dnsutils
```

### From source: Build the latest version

These instructions for building from source are suitable for individual developers who want to build the latest version
of LXD, or build a specific release of LXD which may not be offered by their Linux distribution. Source builds for
integration into Linux distributions are not covered here and may be covered in detail in a separate document in the
future.

```bash
git clone https://github.com/canonical/lxd
cd lxd
```

This will download the current development tree of LXD and place you in the source tree.
Then proceed to the instructions below to actually build and install LXD.

### From source: Build a release

The LXD release tarballs bundle a complete dependency tree as well as a
local copy `libdqlite` for LXD's database setup.

```bash
tar zxvf lxd-4.18.tar.gz
cd lxd-4.18
```

This will unpack the release tarball and place you inside of the source tree.
Then proceed to the instructions below to actually build and install LXD.

### Start the build

The actual building is done by two separate invocations of the Makefile: `make deps` -- which builds libraries required
by LXD -- and `make`, which builds LXD itself. At the end of `make deps`, a message will be displayed which will specify environment variables that should be set prior to invoking `make`. As new versions of LXD are released, these environment
variable settings may change, so be sure to use the ones displayed at the end of the `make deps` process, as the ones
below (shown for example purposes) may not exactly match what your version of LXD requires:

We recommend having at least 2GiB of RAM to allow the build to complete.

```{terminal}
:input: make deps

...
make[1]: Leaving directory '/root/go/deps/dqlite'
# environment

Please set the following in your environment (possibly ~/.bashrc)
#  export CGO_CFLAGS="${CGO_CFLAGS} -I$(go env GOPATH)/deps/dqlite/include/"
#  export CGO_LDFLAGS="${CGO_LDFLAGS} -L$(go env GOPATH)/deps/dqlite/.libs/"
#  export LD_LIBRARY_PATH="$(go env GOPATH)/deps/dqlite/.libs/${LD_LIBRARY_PATH}"
#  export CGO_LDFLAGS_ALLOW="(-Wl,-wrap,pthread_create)|(-Wl,-z,now)"
:input: make
```

### From source: Install

Once the build completes, you simply keep the source tree, add the directory referenced by `$(go env GOPATH)/bin` to
your shell path, and set the `LD_LIBRARY_PATH` variable printed by `make deps` to your environment. This might look
something like this for a `~/.bashrc` file:

```bash
export PATH="${PATH}:$(go env GOPATH)/bin"
export LD_LIBRARY_PATH="$(go env GOPATH)/deps/dqlite/.libs/:${LD_LIBRARY_PATH}"
```

Now, the `lxd` and `lxc` binaries will be available to you and can be used to set up LXD. The binaries will automatically find and use the dependencies built in `$(go env GOPATH)/deps` thanks to the `LD_LIBRARY_PATH` environment variable.

### Machine setup

You'll need sub{u,g}ids for root, so that LXD can create the unprivileged containers:

```bash
echo "root:1000000:1000000000" | sudo tee -a /etc/subuid /etc/subgid
```

By default, only users added to the `lxd` group can interact with the LXD daemon. Installing from source doesn't guarantee that the `lxd` group exists in the system. If you want the current user (or any other user) to be able to interact with the LXD daemon, add it to the `lxd` group:

```bash
getent group lxd >/dev/null || sudo groupadd --system lxd # create the group if needed
getent group lxd | grep -qwF "$USER" || sudo usermod -aG lxd "$USER"
```

```{note}
{{must_start_new_session}}
```

Now you can run the daemon (the `--group sudo` bit allows everyone in the `sudo`
group to talk to LXD; you can create your own group if you want):

```bash
sudo -E PATH=${PATH} LD_LIBRARY_PATH=${LD_LIBRARY_PATH} $(go env GOPATH)/bin/lxd --group sudo
```

```{note}
If `newuidmap/newgidmap` tools are present on your system and `/etc/subuid`, `etc/subgid` exist, they must be configured to allow the root user a contiguous range of at least 10M UID/GID.
```

### Shell completions

Shell completion profiles can be generated with `lxc completion <shell>` (e.g. `lxc completion bash`). Supported shells are `bash`, `zsh`, `fish`, and `powershell`.

```bash
lxc completion bash > /etc/bash_completion.d/lxc # generating completions for bash as an example
. /etc/bash_completion.d/lxc
```

(installing-manage-access)=
## Manage access to LXD

Access control for LXD is based on group membership.
The root user and all members of the `lxd` group can interact with the local daemon.
See {ref}`security-daemon-access` for more information.

On Ubuntu images, the `lxd` group already exists and the main user is automatically added to it. The group is also created during installation if you {ref}`installed LXD from the snap<installing-from-package>`. If the `lxd` group is missing on your system (as might be the case if you {ref}`installed LXD from the sources <installing-from-source>`), create it and restart the LXD daemon:

```bash
getent group lxd >/dev/null || sudo groupadd --system lxd
```

No users are added to the group on installation. You must add trusted users to the group so they can use LXD:

```bash
getent group lxd | grep -qwF "$USER" || sudo usermod -aG lxd "$USER" # adding current user as an example
```

```{note}
{{must_start_new_session}}
```

Anyone added to this group will have full control over LXD. See {ref}`Access to the LXD daemon<security-daemon-access>` to better understand access control for LXD.

Because group membership is normally only applied at login, you might need to either re-open your user session or use the `newgrp lxd` command in the shell you're using to talk to LXD.

````{important}
% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start security note -->
    :end-before: <!-- Include end security note -->
```
````

(installing-upgrade)=
## Upgrade LXD

After upgrading LXD to a newer version, LXD might need to update its database to a new schema.
This update happens automatically when the daemon starts up after a LXD upgrade.
A backup of the database before the update is stored in the same location as the active database (for example, at `/var/snap/lxd/common/lxd/database` for the snap installation).

```{important}
After a schema update, older versions of LXD might regard the database as invalid.
That means that downgrading LXD might render your LXD installation unusable.

In that case, if you need to downgrade, restore the database backup before starting the downgrade.
```
