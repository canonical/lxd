#!/bin/sh
ensure_removed() {
  bad=0
  lxc exec foo -- stat /dev/ttyS0 && bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "device should have been removed; $*"
    false
  fi
}

dounixdevtest() {
    lxc start foo
    lxc config device add foo tty unix-char "$@"
    lxc exec foo -- stat /dev/ttyS0
    lxc exec foo reboot
    lxc exec foo -- stat /dev/ttyS0
    lxc restart foo --force
    lxc exec foo -- stat /dev/ttyS0
    lxc config device remove foo tty
    ensure_removed "was not hot-removed"
    lxc exec foo reboot
    ensure_removed "removed device re-appeared after container reboot"
    lxc restart foo --force
    ensure_removed "removed device re-appaared after lxc reboot"
    lxc stop foo --force
}

testunixdevs() {
  echo "Testing passing char device /dev/ttyS0"
  dounixdevtest path=/dev/ttyS0

  echo "Testing passing char device 4 64"
  dounixdevtest path=/dev/ttyS0 major=4 minor=64
}

ensure_fs_unmounted() {
  bad=0
  lxc exec foo -- stat /mnt/hello && bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "device should have been removed; $*"
    false
  fi
}

testloopmounts() {
  loopfile=$(mktemp -p "${TEST_DIR}" loop_XXX)
  dd if=/dev/zero of="${loopfile}" bs=1M seek=200 count=1
  mkfs.ext4 -F "${loopfile}"

  lpath=$(losetup --show -f "${loopfile}")
  if [ ! -e "${lpath}" ]; then
    echo "failed to setup loop"
    false
  fi
  echo "${lpath}" >> "${TEST_DIR}/loops"

  mkdir -p "${TEST_DIR}/mnt"
  mount "${lpath}" "${TEST_DIR}/mnt" || { echo "loop mount failed"; return; }
  touch "${TEST_DIR}/mnt/hello"
  umount -l "${TEST_DIR}/mnt"
  lxc start foo
  lxc config device add foo mnt disk source="${lpath}" path=/mnt
  lxc exec foo stat /mnt/hello
  # Note - we need to add a set_running_config_item to lxc
  # or work around its absence somehow.  Once that's done, we
  # can run the following two lines:
  #lxc exec foo reboot
  #lxc exec foo stat /mnt/hello
  lxc restart foo --force
  lxc exec foo stat /mnt/hello
  lxc config device remove foo mnt
  ensure_fs_unmounted "fs should have been hot-unmounted"
  lxc exec foo reboot
  ensure_fs_unmounted "removed fs re-appeared after reboot"
  lxc restart foo --force
  ensure_fs_unmounted "removed fs re-appeared after restart"
  lxc stop foo --force
  losetup -d "${lpath}"
  sed -i "\|^${lpath}|d" "${TEST_DIR}/loops"
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

  mkdir -p "${TEST_DIR}/mnt1"
  lxc config device add foo mnt1 disk source="${TEST_DIR}/mnt1" path=/mnt1 readonly=true
  lxc profile create onenic
  lxc profile device add onenic eth0 nic nictype=bridged parent=lxcbr0
  lxc profile apply foo onenic
  lxc profile create unconfined
  lxc profile set unconfined raw.lxc "lxc.aa_profile=unconfined"
  lxc profile apply foo onenic,unconfined

  lxc config device list foo | grep mnt1
  lxc config device show foo | grep "/mnt1"
  lxc config show foo | grep "onenic" -A1 | grep "unconfined"
  lxc profile list | grep onenic
  lxc profile device list onenic | grep eth0
  lxc profile device show onenic | grep lxcbr0

  # test live-adding a nic
  lxc start foo
  ! lxc config show foo | grep -q "raw.lxc"
  lxc config show foo --expanded | grep -q "raw.lxc"
  ! lxc config show foo | grep -v "volatile.eth0" | grep -q "eth0"
  lxc config show foo --expanded | grep -v "volatile.eth0" | grep -q "eth0"
  lxc config device add foo eth2 nic nictype=bridged parent=lxcbr0 name=eth10
  lxc exec foo -- /sbin/ifconfig -a | grep eth0
  lxc exec foo -- /sbin/ifconfig -a | grep eth10
  lxc config device list foo | grep eth2
  lxc config device remove foo eth2

  # test live-adding a disk
  mkdir "${TEST_DIR}/mnt2"
  touch "${TEST_DIR}/mnt2/hosts"
  lxc config device add foo mnt2 disk source="${TEST_DIR}/mnt2" path=/mnt2 readonly=true
  lxc exec foo -- ls /mnt2/hosts
  lxc stop foo --force
  lxc start foo
  lxc exec foo -- ls /mnt2/hosts
  lxc config device remove foo mnt2
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
    false
  fi

  bad=0
  lxc config set foo user.prop 2>/dev/null && bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "property set succeded when it shouldn't have"
    false
  fi

  testunixdevs

  testloopmounts

  lxc delete foo

  lxc init testimage foo
  lxc profile apply foo onenic,unconfined
  lxc start foo

  lxc exec foo -- cat /proc/self/attr/current | grep unconfined
  lxc exec foo -- ls /sys/class/net | grep eth0

  lxc stop foo --force
  lxc delete foo
}
