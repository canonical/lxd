# External QEMU snap for LXD snap

The idea behind this feature is to allow LXD users to build their own snap
containing QEMU and then plug it into the LXD snap, so that LXD uses this
custom QEMU instead of the built-in one.

To make it easier to test this feature and simplify the user experience,
we provide a reference snapcraft.yaml file. It is intended solely as
a starting point for building and customizing your own QEMU snap.

Please note that we do not provide regular or security updates for this
reference example, as it is meant for documentation and educational purposes only.

## How to build and use (example)

```
cd lxd-qemu-snap

# clean up previous builds
rm -f qemu-for-lxd_*.snap

# build
snapcraft pack

# install
sudo snap install qemu-for-lxd_*.snap --devmode

# connect snaps
sudo snap connect lxd:gpu-2404 mesa-2404:gpu-2404
sudo snap connect lxd:qemu-external qemu-for-lxd:qemu-external
```

## How to use with virgl (only specific to this example of snapcraft.yaml)

As part of this external QEMU snap example, we build QEMU with virglrenderer
library support. Of course, this is not required, and in your specific use
case you may choose to omit it. The following instructions show how to verify
that the external QEMU snap with virgl support is working correctly.

```
lxc init images:ubuntu/noble/desktop desktop -c limits.memory=8GiB --vm

# modify instance configuration:
lxc config edit desktop

# choose an appropriate renderer:
ls -la /dev/dri/by-path/*-render

# for example, on my system it is /dev/dri/by-path/pci-0000:67:00.0-render
# lspci | grep -E "(3D|VGA)" shows:
# 67:00.0 VGA compatible controller: Advanced Micro Devices, Inc. [AMD/ATI] Rembrandt (rev d8)

# add the following lines:
  raw.apparmor: |-
    /snap/lxd/*/gpu-2404/** mr,
    /dev/dri/ r,
    /dev/dri/card[0-9]* rw,
    /dev/dri/renderD[0-9]* rw,
    /run/udev/data/c226:[0-9]* r,  # 226 drm
    /sys/devices/** r,
    /sys/bus/** r,
  raw.qemu: -display egl-headless,rendernode=/dev/dri/by-path/pci-0000:67:00.0-render
  raw.qemu.conf: |-
    [device "qemu_gpu"]
    driver = "virtio-vga-gl"

# try it
lxc start desktop --console=vga

# you can check output from:
dmesg | grep -i drm
# if you see:
# [drm] features: +virgl ...
# it's a good sign

glxinfo | grep -i vir
# it should show something like:
# Device: virgl ...
```

## References:

https://github.com/snapcore/snapd/blob/5c8d8431baa425464b279ff26b8c44eecb9aab22/interfaces/builtin/opengl.go#L41

https://gitlab.gnome.org/GNOME/gnome-boxes/-/issues/586