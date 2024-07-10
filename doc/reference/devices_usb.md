(devices-usb)=
# Type: `usb`

```{youtube} https://www.youtube.com/watch?v=SAord28VS4g
:title: LXD USB devices
```

```{note}
The `usb` device type is supported for both containers and VMs.
It supports hotplugging for both containers and VMs.
```

USB devices make the specified USB device appear in the instance.
For performance issues, avoid using devices that require high throughput or low latency.

For containers, only `libusb` devices (at `/dev/bus/usb`) are passed to the instance.
This method works for devices that have user-space drivers.
For devices that require dedicated kernel drivers, use a [`unix-char` device](devices-unix-char) or a [`unix-hotplug` device](devices-unix-hotplug) instead.

For virtual machines, the entire USB device is passed through, so any USB device is supported.
When a device is passed to the instance, it vanishes from the host.

## Device options

`usb` devices have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-unix-usb-device-conf start -->
    :end-before: <!-- config group device-unix-usb-device-conf end -->
```

## Configuration examples

Add a `usb` device to an instance by specifying its vendor ID and product ID:

    lxc config device add <instance_name> <device_name> usb vendorid=<vendor_ID> productid=<product_ID>

To determine the vendor ID and product ID, you can use {command}`lsusb`, for example.

See {ref}`instances-configure-devices` for more information.
