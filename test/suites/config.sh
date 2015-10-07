#!/bin/sh
ensure_removed() {
  bad=0
  lxc exec foo -- stat /dev/lxdkvm && bad=1 || true
  if [ "${bad}" -eq 1 ]; then
    echo "device should have been removed; $*"
    false
  fi
}

dounixdevtest() {
    lxc start foo
    lxc config device add foo kvm unix-char $*
    lxc exec foo -- stat /dev/lxdkvm
    lxc exec foo reboot
    lxc exec foo -- stat /dev/lxdkvm
    lxc restart foo
    lxc exec foo -- stat /dev/lxdkvm
    lxc config device remove foo kvm
    ensure_removed "was not hot-removed"
    lxc exec foo reboot
    ensure_removed "removed device re-appeared after container reboot"
    lxc restart foo
    ensure_removed "removed device re-appaared after lxc reboot"
    lxc stop foo
}

testunixdevs() {
  rm -rf /dev/lxdkvm || true
  if mknod /dev/lxdkvm c 10 232; then
    echo "Testing /dev/lxdkvm"
    dounixdevtest path=/dev/lxdkvm
    rm -f /dev/lxdkvm
  fi
echo "Testing /dev/lxdkvm 10 232"
  dounixdevtest path=/dev/lxdkvm major=10 minor=232
}

test_config_profiles() {
  ensure_import_testimage

  lxc init testimage foo
  lxc profile list | grep default

  # let's check that 'lxc config profile' still works while it's deprecated
  lxc config profile list | grep default

  # setting an invalid config item should error out when setting it, not get
  # into the database and never let the user edit the container again.
  ! lxc config set foo raw.lxc "lxc.notaconfigkey = invalid"

  lxc profile create stdintest
  echo "BADCONF" | lxc profile set stdintest user.user_data -
  lxc profile show stdintest | grep BADCONF
  lxc profile delete stdintest

  echo "BADCONF" | lxc config set foo user.user_data -
  lxc config show foo | grep BADCONF
  lxc config unset foo user.user_data

  lxc config device add foo home disk source=/mnt path=/mnt readonly=true
  lxc profile create onenic
  lxc profile device add onenic eth0 nic nictype=bridged parent=lxcbr0
  lxc profile apply foo onenic
  lxc profile create unconfined
  lxc profile set unconfined raw.lxc "lxc.aa_profile=unconfined"
  lxc profile apply foo onenic,unconfined

  lxc config device list foo | grep home
  lxc config device show foo | grep "/mnt"
  lxc config show foo | grep "onenic" -A1 | grep "unconfined"
  lxc profile list | grep onenic
  lxc profile device list onenic | grep eth0
  lxc profile device show onenic | grep lxcbr0

  # test live-adding a nic
  lxc start foo
  lxc config show foo | grep -q "raw.lxc" && false
  lxc config show foo | grep -v "volatile.eth0.hwaddr" | grep -q "eth0" && false
  lxc config device add foo eth2 nic nictype=bridged parent=lxcbr0 name=eth10
  lxc exec foo -- /sbin/ifconfig -a | grep eth0
  lxc exec foo -- /sbin/ifconfig -a | grep eth10
  lxc config device list foo | grep eth2
  lxc config device remove foo eth2

  # test live-adding a disk
  lxc config device add foo etc disk source=/etc path=/mnt2 readonly=true
  lxc exec foo -- ls /mnt2/hosts
  lxc stop foo --force
  lxc start foo
  lxc exec foo -- ls /mnt2/hosts
  lxc config device remove foo etc
  ! lxc exec foo -- ls /mnt2/hosts
  lxc stop foo --force
  lxc start foo
  ! lxc exec foo -- ls /mnt2/hosts
  lxc stop foo --force

  lxc config set foo user.prop value
  lxc list user.prop=value | grep foo
  lxc config unset foo user.prop

  # Test for invalid raw.lxc
  ! lxc config set foo raw.lxc a
  ! lxc profile set default raw.lxc a

  bad=0
  lxc list user.prop=value | grep foo && bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "property unset failed"
  fi

  bad=0
  lxc config set foo user.prop 2>/dev/null && bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "property set succeded when it shouldn't have"
  fi

  testunixdevs

  lxc delete foo

  lxc init testimage foo
  lxc profile apply foo onenic,unconfined
  lxc start foo

  lxc exec foo -- cat /proc/self/attr/current | grep unconfined
  lxc exec foo -- ls /sys/class/net | grep eth0

  lxc stop foo --force
  lxc delete foo
}
