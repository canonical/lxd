# LXD

LXD is a modern, secure and powerful system container and virtual machine manager.

This is the snap packaging repository that is used to build the [LXD snap](https://snapcraft.io/lxd). The LXD repository is available [here](https://github.com/canonical/lxd).

# Build the LXD snap locally

Local build require the LXD snap to be installed as `snapcraft` creates a container to use as build environment. Here's how to do a local build for the native architecture:

```
snapcraft
```

# Build the LXD snap on Launchpad

To build the snap for multiple architectures, Launchpad builders can be used.

They are available for various architectures (`amd64`, `armhf`, `arm64`, `ppc64el`, `riscv64` and `s390x`) and you can ask for multiple to be built in parallel. Here's how to build for both `amd64` and `arm64`:

```
snapcraft remote-build --launchpad-accept-public-upload --platform amd64,arm64
```

*Note*: if the snap being built is using an older core (like `core22` and earlier), replace `--platform` by `--build-for`.
