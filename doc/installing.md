---
discourse: "[Discourse&#x3a&#32;Building&#32;custom&#32;LXD&#32;binaries&#32;for&#32;side&#32;loading&#32;into&#32;an&#32;existing&#32;snap&#32;installation](37327)"
---

(installing)=
# How to install LXD

````{only} integrated
```{admonition} For MicroCloud users
:class: note
The MicroCloud setup process installs LXD on cluster members. Thus, you do not need to follow the steps on this page.
```
````

There are multiple approaches to installing LXD, depending on your Linux distribution, operating system, and use case.

(installing-snap-package)=
## Install the LXD snap package

The recommended way to install LXD is its [snap package](https://snapcraft.io/lxd), available for many Linux distributions. For alternative methods, see: {ref}`installing-other`, {ref}`installing-other-os`, or {ref}`installing-from-source`.

### Requirements

- The LXD snap must be [available for your Linux distribution](https://snapcraft.io/lxd#distros).
- The [`snapd` daemon](https://snapcraft.io/docs/installing-snapd) must be installed.

### Install

Use this command to install LXD from the recommended {ref}`default snap track <ref-snap-tracks-default>` (currently {{current_lts_track}}):

```bash
sudo snap install lxd
```

If you are installing LXD on a {ref}`cluster member <exp-clusters>`, add the `--cohort="+"` flag to {ref}`keep cluster members synchronized <howto-snap-updates-sync>` to the same snap version:

```bash
sudo snap install lxd --cohort="+"
```

Next, follow the {ref}`installing-snap-post` steps below.

(installing-snap-channel)=
#### Optionally specify a channel

Channels correspond to different {ref}`LXD releases <ref-releases>`. When unspecified, the LXD snap defaults to the most recent `stable` LTS, which is recommended for most use cases.

To specify a different channel, add the `--channel` flag at installation:

```bash
sudo snap install lxd --channel=<target channel> [--cohort="+"]
```

For example, to use the `6/stable` channel, run:

```bash
sudo snap install lxd --channel=6/stable
```

For details about LXD snap channels, see: {ref}`ref-snap-channels`.

(installing-snap-post)=
### Post-installation

Follow these steps after installing the LXD snap.

(installing-snap-user)=
#### Add the current user

To allow the current user to interact with the LXD daemon, update the `lxd` group:

```bash
getent group lxd | grep -qwF "$USER" || sudo usermod -aG lxd "$USER"
```

<!-- Include start newgrp -->
Afterward, apply the change to your current shell session by running:

```bash
newgrp lxd
```

<!-- Include end newgrp -->

For more information, see the {ref}`installing-manage-access` section below.

(installing-snap-hold-updates)=
#### Hold or schedule updates

When a new release is published to a snap channel, installed snaps following that channel update automatically by default.

For {ref}`LXD clusters <exp-clusters>`, or on any machine where you want control over updates, you should override this default behavior by either holding or scheduling updates. See: {ref}`howto-snap-updates`.

(installing-other)=
## Other Linux installation options

Some Linux installations can use package managers other than Snap to install LXD. These managers all install the latest {ref}`feature release <ref-releases-feature>`.

`````{tabs}

````{group-tab} Alpine Linux

Run:

```bash
apk add lxd
```

````

````{group-tab} Arch Linux

Run:

```bash
pacman -S lxd
```

````

````{group-tab} Fedora

Fedora RPM packages for LXC/LXD are available in the [COPR repository](https://copr.fedorainfracloud.org/coprs/ganto/lxc4/). These are unofficial and minimally tested; use at your own risk.

View the [installation guide](https://github.com/ganto/copr-lxc4/wiki) for details.

````

````{group-tab} Gentoo

Run:

```bash
emerge --ask lxd
```

````

`````

Following installation, make sure to {ref}`manage access to LXD <installing-manage-access>`.

(installing-other-os)=
## Other operating systems

Builds of the [`lxc`](lxc.md) client are available for non-Linux operating systems to enable interaction with remote LXD servers. For more information, see: [About `lxd` and `lxc`](lxd-lxc).

`````{tabs}

````{group-tab} macOS

The [Homebrew](https://brew.sh) package manager must be installed on your system.

To install the client from the latest {ref}`feature release <ref-releases-feature>` of LXD, run:

```bash
brew install lxc
```

````

````{group-tab} Windows

The [Chocolatey](https://chocolatey.org) package manager must be installed on your system.

To install the client from the latest {ref}`feature release <ref-releases-feature>` of LXD, run:

```bash
choco install lxc
```

````

`````

(installing-native)=
## Native builds of the client

You can find native builds of the [`lxc`](lxc.md) client on [GitHub](https://github.com/canonical/lxd):

- Linux: [`bin.linux.lxc.aarch64`](https://github.com/canonical/lxd/releases/latest/download/bin.linux.lxc.aarch64), [`bin.linux.lxc.x86_64`](https://github.com/canonical/lxd/releases/latest/download/bin.linux.lxc.x86_64)
- Windows: [`bin.windows.lxc.aarch64.exe`](https://github.com/canonical/lxd/releases/latest/download/bin.windows.lxc.aarch64.exe), [`bin.windows.lxc.x86_64.exe`](https://github.com/canonical/lxd/releases/latest/download/bin.windows.lxc.x86_64.exe)
- macOS: [`bin.macos.lxc.aarch64`](https://github.com/canonical/lxd/releases/latest/download/bin.macos.lxc.aarch64), [`bin.macos.lxc.x86_64`](https://github.com/canonical/lxd/releases/latest/download/bin.macos.lxc.x86_64)

To download a specific build:

1. Make sure that you are logged into your GitHub account.
1. Filter for the branch or tag that you are interested in (for example, the latest release tag or `main`).
1. Select the latest build and download the suitable artifact.

These builds are for the [`lxc`](lxc.md) client only, not the LXD daemon. For an explanation of the differences, see: [About `lxd` and `lxc`](lxd-lxc).

(installing-from-source)=
(installing_from_source)=
## Install LXD from source

These instructions for building and installing from source are suitable for developers who want to build the latest version of LXD, or to build a specific release of LXD which may not be offered by their Linux distribution. Source builds for integration into Linux distributions are not covered.

We recommend having the latest versions of `liblxc` (see {ref}`LXC requirements <requirements-lxc>`)
available for LXD development. For convenience, `make deps` will pull the
appropriate versions of `liblxc` and `dqlite` from their corresponding upstream
Git repository. Additionally, LXD requires a modern Golang (see
{ref}`requirements-go`) version to work. On Ubuntu, you can install these with:

```bash
sudo apt update
sudo apt install \
    autoconf \
    automake \
    build-essential \
    gettext \
    git \
    libacl1-dev \
    libapparmor-dev \
    libcap-dev \
    liblz4-dev \
    libseccomp-dev \
    libsqlite3-dev \
    libtool \
    libudev-dev \
    libuv1-dev \
    make \
    meson \
    ninja-build \
    pkg-config \
    python3-venv
command -v snap >/dev/null || sudo apt-get install snapd
sudo snap install --classic go
```

```{note}
If you use the `liblxc-dev` package and get compile time errors when building the `go-lxc` module,
ensure that the value for `LXC_DEVEL` is `0` for your `liblxc` build. To check this, look at `/usr/include/lxc/version.h`.
If the `LXC_DEVEL` value is `1`, replace it with `0` to work around the problem. It's a packaging bug that is now fixed,
see [LP: #2039873](https://bugs.launchpad.net/ubuntu/+source/lxc/+bug/2039873).
```

There are a few storage drivers for LXD besides the default `dir` driver. Installing these tools adds a bit to `initramfs` and may slow down your host boot, but are needed if you'd like to use a particular driver:

```bash
sudo apt install lvm2 thin-provisioning-tools
sudo apt install btrfs-progs
```

At runtime, LXD might need the following packages to be installed on the host system:

```bash
sudo apt update
sudo apt install \
    attr \
    iproute2 \
    nftables \
    rsync \
    squashfs-tools \
    squashfs-tools-ng \
    tar \
    xz-utils

# `nftables` can be replaced by `iptables` on older systems
```

To run the test suite or test related `make` targets, you'll also need:

```bash
sudo apt update
sudo apt install \
    acl \
    bind9-dnsutils \
    btrfs-progs \
    busybox-static \
    curl \
    dnsmasq-base \
    dosfstools \
    e2fsprogs \
    iputils-ping \
    jq \
    netcat-openbsd \
    openvswitch-switch \
    s3cmd \
    shellcheck \
    socat \
    sqlite3 \
    swtpm \
    xfsprogs \
    yq
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

By default, only users added to the `lxd` group can interact with the LXD daemon. Installing from source doesn't guarantee that the `lxd` group exists in the system. If you want the current user (or any other user) to be able to interact with the LXD daemon, create the group and add the user to it:

```bash
getent group lxd >/dev/null || sudo groupadd --system lxd # create the group if needed
getent group lxd | grep -qwF "$USER" || sudo usermod -aG lxd "$USER"
```

```{include} installing.md
    :start-after: <!-- Include start newgrp -->
    :end-before: <!-- Include end newgrp -->
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

Access control for LXD is based on group membership. The root user and all members of the `lxd` group can interact with the local daemon.

On Ubuntu images, the `lxd` group already exists and the root user is automatically added to it. The group is also created during installation if you {ref}`installed LXD from the snap <installing-snap-package>`.

To check if the `lxd` group exists, run:

```bash
getent group lxd
```

If this command returns no result, the `lxd` group is missing from your system. This might be the case if you {ref}`installed LXD from source <installing-from-source>`. To create the group and restart the LXD daemon, run:

```bash
getent group lxd >/dev/null || sudo groupadd --system lxd
```

Afterward, add trusted users to the group so they can use LXD. The following command adds the current user:

```bash
getent group lxd | grep -qwF "$USER" || sudo usermod -aG lxd "$USER"
```

```{include} installing.md
    :start-after: <!-- Include start newgrp -->
    :end-before: <!-- Include end newgrp -->
```

````{admonition} Important security notice
:class: important
% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start security note -->
    :end-before: <!-- Include end security note -->
```
For more information, see {ref}`security-daemon-access`.
````

(installing-upgrade)=
## Updates and upgrades

For information on updates and upgrades, see the relevant sections in the following guides:

How-to guide:

- {ref}`howto-snap`

Reference:

- {ref}`ref-releases-snap`
