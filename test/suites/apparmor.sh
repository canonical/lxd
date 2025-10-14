_instance_apparmor() {
  if [ ! -e /sys/module/apparmor/ ]; then
    export TEST_UNMET_REQUIREMENT="missing AppArmor kernel support"
    return
  fi

  ensure_import_testimage

  echo "Create and start a test container"
  lxc launch testimage c1
  dmesg -c

  echo "==> Test /sys/kernel/* access"

  echo "Check /sys/kernel/config/"
  echo "Verify read denied"
  ! lxc exec c1 -- ls /sys/kernel/config/ || false

  echo "Check /sys/kernel/debug/"
  echo "Verify read denied"
  ! lxc exec c1 -- ls /sys/kernel/debug/ || false

  echo "Check /sys/kernel/tracing/"
  echo "Verify read denied"
  ! lxc exec c1 -- ls /sys/kernel/tracing/ || false

  echo "Check /sys/kernel/security/"
  echo "Verify read allowed"
  lxc exec c1 -- ls /sys/kernel/security/

  echo "Check /sys/kernel/security/testfile"
  echo "Verify write denied"
  ! lxc exec c1 -- touch /sys/kernel/security/testfile || false

  echo "Check /sys/kernel/security/apparmor/"
  echo "Verify write allowed"
  lxc exec c1 -- touch /sys/kernel/security/apparmor/.load

  echo "==> Test /sys/firmware/efi/efivars/ access"

  if test -d /sys/firmware/efi/efivars/; then
    echo "Verify read denied"
    ! lxc exec c1 -- ls /sys/firmware/efi/efivars/ || false
  else
    echo "    EFI efivars not present (skipping test)"
  fi

  echo "==> Test /proc/sys/kernel access"

  echo "Check /proc/sys/kernel/hostname"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/kernel/hostname

  echo "Check /proc/sys/kernel/domainname"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/kernel/domainname

  echo "Check /proc/sys/kernel/sem"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/kernel/sem

  echo "Check /proc/sys/kernel/shmmax"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/kernel/shmmax

  echo "Check /proc/sys/kernel/msgmax"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/kernel/msgmax

  echo "Check /proc/sys/kernel/kptr_restrict"
  if lxc exec c1 -- test -f /proc/sys/kernel/kptr_restrict; then
    echo "Verify read allowed"
    lxc exec c1 -- cat /proc/sys/kernel/kptr_restrict

    echo "Verify write denied"
    ! echo 0 | lxc exec c1 -- tee /proc/sys/kernel/kptr_restrict || false
  else
    echo "    kptr_restrict not available (skipping test)"
  fi

  echo "==> Test /proc access"

  echo "Check /proc/stat"
  echo "Verify read allowed"
  lxc exec c1 -- grep -wm1 "^cpu" /proc/stat

  echo "Check /proc/meminfo"
  echo "Verify read allowed"
  lxc exec c1 -- grep -wm1 "^MemTotal:" /proc/meminfo

  echo "Check /proc/uptime"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/uptime

  echo "Check /proc/kcore"
  echo "Verify read denied"
  ! lxc exec c1 -- cat /proc/kcore || false

  echo "Check /proc/sysrq-trigger"
  echo "Verify write denied"
  ! echo 1 | lxc exec c1 -- tee /proc/sysrq-trigger || false

  echo "Verify read denied"
  ! lxc exec c1 -- cat /proc/sysrq-trigger || false

  echo "Check /proc/acpi/**"
  echo "Verify read denied"
  ! lxc exec c1 -- cat /proc/acpi/wakeup || false

  echo "==> Test /proc/sys access"

  echo "Check /proc/sys/fs/file-max"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/fs/file-max

  echo "Verify write denied"
  ! echo 0 | lxc exec c1 -- tee /proc/sys/fs/file-max || false

  echo "==> Test /proc/sys/net/* access"

  echo "Check /proc/sys/net/ipv4/ip_forward"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/net/ipv4/ip_forward

  echo "Verify write allowed"
  echo 1 | lxc exec c1 -- tee /proc/sys/net/ipv4/ip_forward

  echo "==> Test /proc/sys/fs/binfmt_misc access"

  echo "Verify binfmt_misc is supported"
  if lxc info | grep -F 'unpriv_binfmt: "true"'; then
    echo "    binfmt_misc is supported (unpriv_binfmt enabled)"

    echo "Verify mount allowed"
    lxc exec c1 -- mount -t binfmt_misc none /proc/sys/fs/binfmt_misc
  echo "Verify binfmt_misc status is enabled"
    [ "$(lxc exec c1 -- cat /proc/sys/fs/binfmt_misc/status)" = "enabled" ]
  else
    echo "    binfmt_misc not supported (unpriv_binfmt disabled)"
  fi

  echo "dmesg output"
  journalctl --quiet --no-hostname --no-pager --boot=0 --lines=100 --dmesg

  # Cleanup
  lxc delete c1 --force
}

test_apparmor() {
  _instance_apparmor
}

test_snap_apparmor() {
  _instance_apparmor
}
