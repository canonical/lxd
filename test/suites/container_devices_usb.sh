test_container_devices_usb() {
  ensure_import_testimage

  ctName="usb$$"

  lxc launch testimage "${ctName}"

  lxc config device add "${ctName}" usb0 usb vendorid=dead productid=beef

  # No matching device is present yet.
  ! lxc exec "${ctName}" -- test -e /dev/bus/usb/009/099 || false

  sub_test "Hotplugging usb device"
  deps/uevent add

  for _ in $(seq 1 100); do
    if lxc exec "${ctName}" -- test -c /dev/bus/usb/009/099 2>/dev/null; then
      break
    fi

    sleep 0.1
  done

  [ "$(lxc exec "${ctName}" -- stat -c '%F %t %T' /dev/bus/usb/009/099)" = "character special file bd 63" ]

  sub_test "Unplugging usb device"
  deps/uevent remove

  for _ in $(seq 1 100); do
    if ! lxc exec "${ctName}" -- test -e /dev/bus/usb/009/099 2>/dev/null; then
      break
    fi

    sleep 0.1
  done

  ! lxc exec "${ctName}" -- test -e /dev/bus/usb/009/099 || false

  lxc delete -f "${ctName}"
}
