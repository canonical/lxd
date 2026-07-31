# CI status

Tests | GPU Passthrough | Lint
:---: | :---: | :---:
[![Tests](https://github.com/canonical/lxd-ci/actions/workflows/tests.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/tests.yml) | [![GPU Passthrough](https://github.com/canonical/lxd-ci/actions/workflows/gpu-passthrough-tests.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/gpu-passthrough-tests.yml) | [![Lint](https://github.com/canonical/lxd-ci/actions/workflows/lint.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/lint.yml)

## Images

RH-based | Debian-based | Others
:---: | :---: | :---:
[![almalinux](https://github.com/canonical/lxd-ci/actions/workflows/image-almalinux.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-almalinux.yml) | [![debian](https://github.com/canonical/lxd-ci/actions/workflows/image-debian.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-debian.yml) | [![alpine](https://github.com/canonical/lxd-ci/actions/workflows/image-alpine.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-alpine.yml)
[![alt](https://github.com/canonical/lxd-ci/actions/workflows/image-alt.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-alt.yml) | [![devuan](https://github.com/canonical/lxd-ci/actions/workflows/image-devuan.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-devuan.yml) | [![archlinux](https://github.com/canonical/lxd-ci/actions/workflows/image-archlinux.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-archlinux.yml)
[![amazonlinux](https://github.com/canonical/lxd-ci/actions/workflows/image-amazonlinux.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-amazonlinux.yml) | [![kali](https://github.com/canonical/lxd-ci/actions/workflows/image-kali.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-kali.yml) | [![busybox](https://github.com/canonical/lxd-ci/actions/workflows/image-busybox.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-busybox.yml)
[![centos](https://github.com/canonical/lxd-ci/actions/workflows/image-centos.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-centos.yml) | [![mint](https://github.com/canonical/lxd-ci/actions/workflows/image-mint.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-mint.yml) | [![gentoo](https://github.com/canonical/lxd-ci/actions/workflows/image-gentoo.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-gentoo.yml)
[![fedora](https://github.com/canonical/lxd-ci/actions/workflows/image-fedora.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-fedora.yml) | [![ubuntu](https://github.com/canonical/lxd-ci/actions/workflows/image-ubuntu.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-ubuntu.yml) | [![opensuse](https://github.com/canonical/lxd-ci/actions/workflows/image-opensuse.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-opensuse.yml)
[![openeuler](https://github.com/canonical/lxd-ci/actions/workflows/image-openeuler.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-openeuler.yml) |   | [![openwrt](https://github.com/canonical/lxd-ci/actions/workflows/image-openwrt.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-openwrt.yml)
[![oracle](https://github.com/canonical/lxd-ci/actions/workflows/image-oracle.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-oracle.yml) |   | [![slackware](https://github.com/canonical/lxd-ci/actions/workflows/image-slackware.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-slackware.yml)
[![rockylinux](https://github.com/canonical/lxd-ci/actions/workflows/image-rockylinux.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-rockylinux.yml) |   | [![voidlinux](https://github.com/canonical/lxd-ci/actions/workflows/image-voidlinux.yml/badge.svg)](https://github.com/canonical/lxd-ci/actions/workflows/image-voidlinux.yml)

Those community maintained images end up in the [`images:` remote](https://images.lxd.canonical.com/).

# Prepare VM to run tests locally

To run the tests locally, it's ideal to run them in a shortlived VM. The simplest way is to create a `lxd-ci` profile that you then use when creating the shortlived VM.

## lxd-ci profile

For convenience, a special profile (`lxd-ci`) can be used to run tests in local VMs. To define that profile:

```sh
# this needs to be run from inside the git repostory
GIT_ROOT="$(git rev-parse --show-toplevel)"
# create or edit the profile based on the provided template
lxc profile list | grep -qwF lxd-ci || lxc profile create lxd-ci
sed "s|@@PATH_TO_LXD_CI_GIT@@|${GIT_ROOT}|" "${GIT_ROOT}/lxd-ci.yaml" | lxc profile edit lxd-ci
```

Then it's easy to create a shortlived VM:

```sh
lxc launch ubuntu-minimal-daily:24.04 v1 --vm -p lxd-ci
```

```sh
$ lxc shell v1
root@v1:~# cd lxd-ci/
root@v1:~/lxd-ci# ./bin/local-run tests/snapd latest/edge
```

# Running tests locally

To run a test locally (directly where you invoke it), use the `bin/local-run` helper:

```sh
./bin/local-run tests/interception latest/edge
```

For faster repeated runs, you might want to tell `snap` that it can purge the LXD snap
without taking any snapshot:

```sh
PURGE_LXD=1 ./bin/local-run tests/interception latest/edge
```

To test a with the exising/already installed LXD snap, you can set the `KEEP_LXD` environment variable.

```sh
KEEP_LXD=1 ./bin/local-run tests/interception latest/edge
```

Note: if you need to run tests on temporary machines, [Testflinger reservations](https://docs.google.com/document/d/11Kot68mnBY9Wq9DXRzTVrKpx5cMkkhBC5RrM51eyybY) might be useful.

To test a custom build of LXD, you can set the `LXD_SIDELOAD_PATH` environment variable.
This will be copied to `/var/snap/lxd/common/lxd.debug` and the daemon will be reloaded before the test run.

```sh
LXD_SIDELOAD_PATH=/tmp/lxd ./bin/local-run tests/interception latest/edge
```

To also test a custom build of `lxc`, you can set the `LXC_SIDELOAD_PATH` environment variable.
This will be copied to `/var/snap/lxd/common/lxc.debug` and used by the tests.

```sh
LXC_SIDELOAD_PATH=/tmp/lxc ./bin/local-run tests/interception latest/edge
```

To test a custom snap of LXD, you can set the `LXD_SNAP_PATH` environment variable.

```sh
LXD_SNAP_PATH=/tmp/lxd_0+git.89550582_amd64.snap ./bin/local-run tests/interception latest/edge
```

To use the system's ZFS tools, you can set the `LXD_ZFS_EXTERNAL` environment variable.

```sh
LXD_ZFS_EXTERNAL=1 ./bin/local-run tests/interception latest/edge
```

To run `tests/network-ovn` against various OVN implementation:

```sh
# Using the deb package from the base Os
OVN_SOURCE=deb PURGE_LXD=1 ./bin/local-run tests/network-ovn latest/edge

# Use numbered releases of MicroOVN
OVN_SOURCE=22.03/edge PURGE_LXD=1 ./bin/local-run tests/network-ovn latest/edge
OVN_SOURCE=24.03/edge PURGE_LXD=1 ./bin/local-run tests/network-ovn latest/edge

# Using the `latest/edge` MicroOVN snap channel
PURGE_LXD=1 ./bin/local-run tests/network-ovn latest/edge
```

# Running tests on OpenStack (ProdStack)

The tests need to be run from PS6's LXD bastion `lxd-bastion-ps6.internal`. Once connected, the proper environment can be loaded with:

```sh
pe
```

Then to run all the tests on OpenStack VMs:

```sh
./tests/main-openstack
```

Or to run individual tests (`tests/pylxd` against `latest/edge`):

```sh
# bin/openstack-run: <serie> <kernel> <test> <args>
./bin/openstack-run jammy default tests/pylxd latest/edge
```

# Running Dell PowerFlex VM storage tests

To run the VM storage tests on the Dell PowerFlex driver, provide the following environment variables:

* `POWERFLEX_POOL`: Name of the PowerFlex storage pool
* `POWERFLEX_DOMAIN`: Name of the PowerFlex domain
* `POWERFLEX_GATEWAY`: Address of the PowerFlex HTTP gateway
* `POWERFLEX_GATEWAY_VERIFY`: Whether to verify the HTTP gateway's certificate. The default is `true`.
* `POWERFLEX_USER`: Name of the PowerFlex user
* `POWERFLEX_PASSWORD`: Password of the PowerFlex user
* `POWERFLEX_MODE`: Operation mode for the consumption of storage volumes. The default is `nvme/tcp`.
* `POWERFLEX_SNAPSHOT_COPY`: If enabled, the driver uses PowerFlex snapshots for optimized copy

Use a PowerFlex storage pool (`POWERFLEX_POOL`) which has zero-padding enabled.
Using non zero-padding enabled pools is not allowed.

# Running Dell PowerStore VM storage tests

To run the VM storage tests using Dell PowerStore driver, provide the following environment variables:

* `POWERSTORE_GATEWAY`: Address of the PowerStore gateway.
* `POWERSTORE_GATEWAY_VERIFY`: Whether to verify the HTTP gateway's certificate. The default is `true`.
* `POWERSTORE_USER`: Name of the PowerStore user.
* `POWERSTORE_PASSWORD`: Password for PowerStore user.
* `POWERSTORE_MODE`: Operation mode for the consumption of storage volumes. The default is `nvme/tcp`.

# Running Pure Storage VM storage tests

To run the VM storage tests using Pure Storage driver, provide the following environment variables:

* `PURE_GATEWAY`: Address of the Pure Storage HTTP gateway
* `PURE_GATEWAY_VERIFY`: Whether to verify the HTTP gateway's certificate. The default is `true`.
* `PURE_API_KEY`: Pure Storage API key.
* `PURE_MODE`: Operation mode for the consumption of storage volumes. The default is `nvme/tcp`.

# Running HPE Alletra Storage VM storage tests

To run the VM storage tests using HPE Alletra Storage driver, provide the following environment variables:

* `ALLETRA_WSAPI`: Address of the HTTP WSAPI gateway
* `ALLETRA_WSAPI_VERIFY`: Whether to verify the HTTP WSAPI gateway's certificate. The default is `true`.
* `ALLETRA_USERNAME`: Name of the user
* `ALLETRA_PASSWORD`: Password of the user
* `ALLETRA_CPG`: Common Provisioning Group (CPG) name
* `ALLETRA_MODE`: Operation mode for the consumption of storage volumes. The default is `nvme/tcp`.

# Infrastructure managed by IS

The PS6 environment has inbound and outbound firewalling applied at the network edge. In order to access some external sites here are the firewall rules we added to firewall maintained by IS:

https://code.launchpad.net/~sdeziel/canonical-is-firewalls/+git/canonical-is-firewalls/+merge/446061

Some HTTP(S) destination also require going through a proxy maintained by IS, here are the proxy rules we added:

https://code.launchpad.net/~sdeziel/canonical-is-internal-proxy-configs/+git/canonical-is-internal-proxy-configs/+merge/446187
