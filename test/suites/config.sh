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
    lxc restart foo
    lxc exec foo -- stat /dev/ttyS0
    lxc restart foo --force
    lxc exec foo -- stat /dev/ttyS0
    lxc config device remove foo tty
    ensure_removed "was not hot-removed"
    lxc restart foo
    ensure_removed "removed device re-appeared after container reboot"
    lxc restart foo --force
    ensure_removed "removed device re-appaared after lxc reboot"
    lxc stop foo --force
}

testunixdevs() {
  if [ ! -e /dev/ttyS0 ] || [ ! -e /dev/ttyS1 ]; then
     echo "==> SKIP: /dev/ttyS0 or /dev/ttyS1 are missing"
     return
  fi

  echo "Testing passing char device /dev/ttyS0"
  dounixdevtest path=/dev/ttyS0

  echo "Testing passing char device 4 64"
  dounixdevtest path=/dev/ttyS0 major=4 minor=64

  echo "Testing passing char device source=/dev/ttyS0"
  dounixdevtest source=/dev/ttyS0

  echo "Testing passing char device path=/dev/ttyS0 source=/dev/ttyS0"
  dounixdevtest path=/dev/ttyS0 source=/dev/ttyS0

  echo "Testing passing char device path=/dev/ttyS0 source=/dev/ttyS1"
  dounixdevtest path=/dev/ttyS0 source=/dev/ttyS1
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
  lxc restart foo
  ensure_fs_unmounted "removed fs re-appeared after reboot"
  lxc restart foo --force
  ensure_fs_unmounted "removed fs re-appeared after restart"
  lxc stop foo --force
  losetup -d "${lpath}"
  sed -i "\\|^${lpath}|d" "${TEST_DIR}/loops"
}

test_mount_order() {
  mkdir -p "${TEST_DIR}/order/empty"
  mkdir -p "${TEST_DIR}/order/full"
  touch "${TEST_DIR}/order/full/filler"

  # The idea here is that sometimes (depending on how golang randomizes the
  # config) the empty dir will have the contents of full in it, but sometimes
  # it won't depending on whether the devices below are processed in order or
  # not. This should not be racy, and they should *always* be processed in path
  # order, so the filler file should always be there.
  lxc config device add foo order disk source="${TEST_DIR}/order" path=/mnt
  lxc config device add foo orderFull disk source="${TEST_DIR}/order/full" path=/mnt/empty

  lxc start foo
  lxc exec foo -- cat /mnt/empty/filler
  lxc stop foo --force
}

test_config_profiles() {
  ensure_import_testimage

  lxc init testimage foo -s "lxdtest-$(basename "${LXD_DIR}")"
  lxc profile list | grep default

  # let's check that 'lxc config profile' still works while it's deprecated
  lxc config profile list | grep default

  # setting an invalid config item should error out when setting it, not get
  # into the database and never let the user edit the container again.
  ! lxc config set foo raw.lxc lxc.notaconfigkey=invalid

  # check that various profile application mechanisms work
  lxc profile create one
  lxc profile create two
  lxc profile assign foo one,two
  [ "$(lxc info foo | grep Profiles)" = "Profiles: one, two" ]
  lxc profile assign foo ""
  [ "$(lxc info foo | grep Profiles)" = "Profiles: " ]
  lxc profile apply foo one # backwards compat check with `lxc profile apply`
  [ "$(lxc info foo | grep Profiles)" = "Profiles: one" ]
  lxc profile assign foo ""
  lxc profile add foo one
  [ "$(lxc info foo | grep Profiles)" = "Profiles: one" ]
  lxc profile remove foo one
  [ "$(lxc info foo | grep Profiles)" = "Profiles: " ]

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
  lxc profile device add onenic eth0 nic nictype=p2p
  lxc profile assign foo onenic
  lxc profile create unconfined

  # Look at the LXC version to decide whether to use the new
  # or the new config key for apparmor.
  lxc_version=$(lxc info | grep "driver_version: " | cut -d' ' -f4)
  lxc_major=$(echo "${lxc_version}" | cut -d. -f1)
  lxc_minor=$(echo "${lxc_version}" | cut -d. -f2)
  if [ "${lxc_major}" -lt 2 ] || { [ "${lxc_major}" = "2" ] && [ "${lxc_minor}" -lt "1" ]; }; then
      lxc profile set unconfined raw.lxc "lxc.aa_profile=unconfined"
  else
      lxc profile set unconfined raw.lxc "lxc.apparmor.profile=unconfined"
  fi

  lxc profile assign foo onenic,unconfined

  # test profile rename
  lxc profile create foo
  lxc profile rename foo bar
  lxc profile list | grep -qv foo  # the old name is gone
  lxc profile delete bar

  lxc config device list foo | grep mnt1
  lxc config device show foo | grep "/mnt1"
  lxc config show foo | grep "onenic" -A1 | grep "unconfined"
  lxc profile list | grep onenic
  lxc profile device list onenic | grep eth0
  lxc profile device show onenic | grep p2p

  # test live-adding a nic
  lxc start foo
  lxc exec foo -- cat /proc/self/mountinfo | grep -q "/mnt1.*ro,"
  ! lxc config show foo | grep -q "raw.lxc" || false
  lxc config show foo --expanded | grep -q "raw.lxc"
  ! lxc config show foo | grep -v "volatile.eth0" | grep -q "eth0" || false
  lxc config show foo --expanded | grep -v "volatile.eth0" | grep -q "eth0"
  lxc config device add foo eth2 nic nictype=p2p name=eth10
  lxc exec foo -- /sbin/ifconfig -a | grep eth0
  lxc exec foo -- /sbin/ifconfig -a | grep eth10
  lxc config device list foo | grep eth2
  lxc config device remove foo eth2

  # test live-adding a disk
  mkdir "${TEST_DIR}/mnt2"
  touch "${TEST_DIR}/mnt2/hosts"
  lxc config device add foo mnt2 disk source="${TEST_DIR}/mnt2" path=/mnt2 readonly=true
  lxc exec foo -- cat /proc/self/mountinfo | grep -q "/mnt2.*ro,"
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
    echo "property set succeeded when it shouldn't have"
    false
  fi

  testunixdevs

  testloopmounts

  test_mount_order

  lxc delete foo

  lxc init testimage foo -s "lxdtest-$(basename "${LXD_DIR}")"
  lxc profile assign foo onenic,unconfined
  lxc start foo

  if [ -e /sys/module/apparmor ]; then
    lxc exec foo -- cat /proc/self/attr/current | grep unconfined
  fi
  lxc exec foo -- ls /sys/class/net | grep eth0

  lxc stop foo --force
  lxc delete foo
}


test_config_edit() {
    ensure_import_testimage

    lxc init testimage foo -s "lxdtest-$(basename "${LXD_DIR}")"
    lxc config show foo | sed 's/^description:.*/description: bar/' | lxc config edit foo
    lxc config show foo | grep -q 'description: bar'
    lxc delete foo
}

test_config_edit_container_snapshot_pool_config() {
    # shellcheck disable=2034,2039,2155
    local storage_pool="lxdtest-$(basename "${LXD_DIR}")"

    ensure_import_testimage

    lxc init testimage c1 -s "$storage_pool"
    lxc snapshot c1 s1
    # edit the container volume name
    lxc storage volume show "$storage_pool" container/c1 | \
        sed 's/^description:.*/description: bar/' | \
        lxc storage volume edit "$storage_pool" container/c1
    lxc storage volume show "$storage_pool" container/c1 | grep -q 'description: bar'
    # edit the container snapshot volume name
    lxc storage volume show "$storage_pool" container/c1/s1 | \
        sed 's/^description:.*/description: baz/' | \
        lxc storage volume edit "$storage_pool" container/c1/s1
    lxc storage volume show "$storage_pool" container/c1/s1 | grep -q 'description: baz'
    lxc delete c1
}

test_container_metadata() {
    ensure_import_testimage
    lxc init testimage c

    # metadata for the container are printed
    lxc config metadata show c | grep -q Busybox

    # metadata can be edited
    lxc config metadata show c | sed 's/Busybox/BB/' | lxc config metadata edit c
    lxc config metadata show c | grep -q BB

    # templates can be listed
    lxc config template list c | grep -q template.tpl

    # template content can be returned
    lxc config template show c template.tpl | grep -q "name:"

    # templates can be added
    lxc config template create c my.tpl
    lxc config template list c | grep -q my.tpl

    # template content can be updated
    echo "some content" | lxc config template edit c my.tpl
    lxc config template show c my.tpl | grep -q "some content"

    # templates can be removed
    lxc config template delete c my.tpl
    ! lxc config template list c | grep -q my.tpl || false

    lxc delete c
}
