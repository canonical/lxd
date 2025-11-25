_ensure_removed() {
  if lxc exec foo -- stat /dev/ttyS0; then
    echo "device should have been removed; $*"
    false
  fi
}

_unix_dev_test() {
    lxc start foo
    lxc config device add foo tty unix-char "$@"
    lxc exec foo -- stat /dev/ttyS0
    lxc restart foo --force
    lxc exec foo -- stat /dev/ttyS0
    lxc config device remove foo tty
    _ensure_removed "was not hot-removed"
    lxc restart foo --force
    _ensure_removed "removed device re-appeared after container reboot"
    lxc stop foo --force
}

_unix_devs() {
  if [ ! -e /dev/ttyS0 ] || [ ! -e /dev/ttyS1 ]; then
     echo "==> SKIP: /dev/ttyS0 or /dev/ttyS1 are missing"
     return
  fi

  echo "Testing passing char device /dev/ttyS0"
  _unix_dev_test path=/dev/ttyS0

  echo "Testing passing char device 4 64"
  _unix_dev_test path=/dev/ttyS0 major=4 minor=64

  echo "Testing passing char device source=/dev/ttyS0"
  _unix_dev_test source=/dev/ttyS0

  echo "Testing passing char device path=/dev/ttyS0 source=/dev/ttyS0"
  _unix_dev_test path=/dev/ttyS0 source=/dev/ttyS0

  echo "Testing passing char device path=/dev/ttyS0 source=/dev/ttyS1"
  _unix_dev_test path=/dev/ttyS0 source=/dev/ttyS1
}

_ensure_fs_unmounted() {
  if lxc exec foo -- mountpoint /mnt/hello; then
    echo "device should have been removed; $*"
    false
  fi
}

_loop_mounts() {
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
  lxc exec foo -- stat /mnt/hello
  # Note - we need to add a set_running_config_item to lxc
  # or work around its absence somehow.  Once that's done, we
  # can run the following two lines:
  #lxc exec foo -- reboot
  #lxc exec foo -- stat /mnt/hello
  lxc restart foo --force
  lxc exec foo -- stat /mnt/hello
  lxc config device remove foo mnt
  _ensure_fs_unmounted "fs should have been hot-unmounted"
  lxc restart foo --force
  _ensure_fs_unmounted "removed fs re-appeared after restart"
  lxc stop foo --force
  losetup -d "${lpath}"
  sed -i "\\|^${lpath}|d" "${TEST_DIR}/loops"
}

_mount_order() {
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
  # Unset LXD_DEVMONITOR_DIR as this test uses devices in /dev instead of TEST_DIR.
  unset LXD_DEVMONITOR_DIR
  shutdown_lxd "${LXD_DIR}"
  respawn_lxd "${LXD_DIR}" true

  ensure_import_testimage

  lxc init testimage foo -s "lxdtest-$(basename "${LXD_DIR}")"
  lxc profile list | grep default

  # let's check that 'lxc config profile' still works while it's deprecated
  lxc config profile list | grep default

  # setting an invalid config item should error out when setting it, not get
  # into the database and never let the user edit the container again.
  ! lxc config set foo raw.lxc lxc.notaconfigkey=invalid || false

  # validate unsets
  lxc profile set default user.foo bar
  lxc profile show default | grep -F user.foo
  lxc profile unset default user.foo
  ! lxc profile show default | grep -F user.foo || false

  lxc profile device set default eth0 limits.egress 100Mbit
  lxc profile show default | grep -F limits.egress
  lxc profile device unset default eth0 limits.egress
  ! lxc profile show default | grep -F limits.egress || false

  # check that various profile application mechanisms work
  lxc profile create one
  lxc profile create two
  lxc profile assign foo one,two
  [ "$(lxc list -f json foo | jq -r '.[0].profiles | join(" ")')" = "one two" ]
  lxc profile assign foo ""
  [ "$(lxc list -f json foo | jq -r '.[0].profiles | join(" ")')" = "" ]
  lxc profile apply foo one # backwards compat check with `lxc profile apply`
  [ "$(lxc list -f json foo | jq -r '.[0].profiles | join(" ")')" = "one" ]
  lxc profile assign foo ""
  lxc profile add foo one
  [ "$(lxc list -f json foo | jq -r '.[0].profiles | join(" ")')" = "one" ]
  lxc profile remove foo one
  [ "$(lxc list -f json foo | jq -r '.[0].profiles | join(" ")')" = "" ]

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

  lxc profile set unconfined raw.lxc "lxc.apparmor.profile=unconfined"

  lxc profile assign foo onenic,unconfined

  # test profile rename
  lxc profile create foo
  lxc profile rename foo bar
  ! lxc profile list | grep -wF foo || false  # the old name is gone
  lxc profile delete bar

  lxc config device list foo | grep mnt1
  lxc config device show foo | grep "/mnt1"
  lxc config show foo | grep "onenic" -A1 | grep "unconfined"
  lxc profile list | grep onenic
  lxc profile device list onenic | grep eth0
  lxc profile device show onenic | grep p2p

  # test setting limits.cpu.pin_strategy at the local config and profile level
  ! lxc config set c1 limits.cpu.pin_strategy=auto || false
  lxc profile set default limits.cpu.pin_strategy=auto
  ! lxc profile set default limits.cpu=1-2 || false # test adding a cpu limit with limits.cpu.pin_strategy set (should fail)
  lxc profile unset default limits.cpu.pin_strategy

  # test live-adding a nic
  veth_host_name="veth$$"
  lxc start foo
  lxc exec foo -- cat /proc/self/mountinfo | grep "/mnt1.*ro,"
  ! lxc config show foo | grep -F "raw.lxc" || false
  lxc config show foo --expanded | grep -F "raw.lxc"
  ! lxc config show foo | grep -vF "volatile.eth0" | grep -F "eth0" || false
  lxc config show foo --expanded | grep -vF "volatile.eth0" | grep -F "eth0"
  lxc config device add foo eth2 nic nictype=p2p name=eth10 host_name="${veth_host_name}"
  lxc exec foo -- /sbin/ifconfig -a | grep eth0
  lxc exec foo -- /sbin/ifconfig -a | grep eth10
  lxc config device list foo | grep eth2
  lxc config device remove foo eth2

  # test live-adding a disk
  mkdir "${TEST_DIR}/mnt2"
  touch "${TEST_DIR}/mnt2/hosts"
  lxc config device add foo mnt2 disk source="${TEST_DIR}/mnt2" path=/mnt2 readonly=true
  lxc exec foo -- cat /proc/self/mountinfo | grep "/mnt2.*ro,"
  lxc exec foo -- ls /mnt2/hosts
  lxc stop foo --force
  lxc start foo
  lxc exec foo -- ls /mnt2/hosts
  lxc config device remove foo mnt2
  ! lxc exec foo -- ls /mnt2/hosts || false
  lxc stop foo --force
  lxc start foo
  ! lxc exec foo -- ls /mnt2/hosts || false
  lxc stop foo --force

  lxc config set foo user.prop value
  [ "$(lxc list -f csv -c n user.prop=value)" = "foo" ]
  lxc config unset foo user.prop

  # Test for invalid raw.lxc
  ! lxc config set foo raw.lxc a || false
  ! lxc profile set default raw.lxc a || false

  if [ "$(lxc list -f csv -c n user.prop=value)" != "" ]; then
    echo "property unset failed"
    false
  fi

  if lxc config set foo user.prop 2>/dev/null; then
    echo "property set succeeded when it shouldn't have"
    false
  fi

  # Test unsetting config keys
  lxc config set core.metrics_authentication false
  [ "$(lxc config get core.metrics_authentication)" = "false" ]

  lxc config unset core.metrics_authentication
  [ -z "$(lxc config get core.metrics_authentication)" ]

  # Validate user.* keys
  ! lxc config set user.⍾ foo || false
  lxc config set user.foo bar
  lxc config unset user.foo

  _unix_devs

  _loop_mounts

  _mount_order

  lxc delete foo

  lxc init testimage foo -s "lxdtest-$(basename "${LXD_DIR}")"
  lxc profile assign foo onenic,unconfined
  lxc start foo

  if [ -e /sys/module/apparmor ]; then
    [ "$(lxc exec foo -- cat /proc/self/attr/current)" = "unconfined" ]
  fi
  lxc exec foo -- ls /sys/class/net | grep eth0

  lxc stop foo --force
  lxc delete foo
}


test_config_edit() {
    if ! tty -s; then
        echo "==> SKIP: test_config_edit requires a terminal"
        return
    fi

    lxc init --empty foo
    lxc config set foo --property description="hello"
    lxc config show foo | sed 's/^description:.*/description: bar/' | lxc config edit foo
    [ "$(lxc config get foo --property description)" = "bar" ]

    # Check instance name is included in edit screen.
    cmd=$(unset -f lxc; command -v lxc)
    output=$(EDITOR="cat" timeout --foreground 120 "${cmd}" config edit foo)
    echo "${output}" | grep "name: foo"

    # Check expanded config isn't included in edit screen.
    ! echo "${output}" | grep "expanded" || false

    lxc delete foo
}

test_property() {
  ensure_import_testimage

  lxc init --empty foo

  # Set a property of an instance
  lxc config set foo description="a new description" --property
  # Check that the property is set
  [ "$(lxc config get foo description --property)" = "a new description" ]

  # Unset a property of an instance
  lxc config unset foo description --property
  # Check that the property is unset
  [ "$(lxc config get foo description --property)" = "" ]

  # Set a property of an instance (bool)
  lxc config set foo ephemeral=true --property
  # Check that the property is set
  [ "$(lxc config get foo ephemeral --property)" = "true" ]

  # Unset a property of an instance (bool)
  lxc config unset foo ephemeral --property
  # Check that the property is unset (i.e false)
  [ "$(lxc config get foo ephemeral --property)" = "false" ]

  # Create a snap of the instance to set its expiration timestamp
  lxc snapshot foo s1
  lxc config set foo/s1 expires_at="2038-03-23T17:38:37.753398689-04:00" --property
  [ "$(lxc config get foo/s1 expires_at --property)" = "2038-03-23 17:38:37.753398689 -0400 -0400" ]
  lxc config show foo/s1 | grep -F "expires_at: 2038-03-23T17:38:37.753398689-04:00"
  lxc config unset foo/s1 expires_at --property
  lxc config show foo/s1 | grep -F "expires_at: 0001-01-01T00:00:00Z"
  lxc delete -f foo

  # Create a storage volume, create a volume snapshot and set its expiration timestamp
  local storage_pool
  storage_pool="lxdtest-$(basename "${LXD_DIR}")"
  storage_volume="${storage_pool}-vol"

  lxc storage volume create "${storage_pool}" "${storage_volume}" size=1MiB
  lxc launch testimage c1 -s "${storage_pool}"

  # This will create a snapshot named 'snap0'
  lxc storage volume snapshot "${storage_pool}" "${storage_volume}"

  lxc storage volume set "${storage_pool}" "${storage_volume}"/snap0 expires_at="2038-03-23T17:38:37.753398689-04:00" --property
  lxc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep 'expires_at: 2038-03-23T17:38:37.753398689-04:00'
  lxc storage volume unset "${storage_pool}" "${storage_volume}"/snap0 expires_at --property
  lxc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep 'expires_at: 0001-01-01T00:00:00Z'

  # Toggle the ephemeral flag on a running instance and check that it is deleted on stop
  lxc config set c1 ephemeral=true --property
  [ "$(lxc config get c1 ephemeral --property)" = "true" ]
  lxc stop -f c1
  [ "$(lxc list -f csv -c n)" = "" ]

  lxc storage volume delete "${storage_pool}" "${storage_volume}"
}

test_config_edit_container_snapshot_pool_config() {
    local storage_pool
    storage_pool="lxdtest-$(basename "${LXD_DIR}")"

    ensure_import_testimage

    lxc init testimage c1 -s "$storage_pool"
    lxc snapshot c1 s1
    # edit the container volume name
    lxc storage volume show "$storage_pool" container/c1 | \
        sed 's/^description:.*/description: bar/' | \
        lxc storage volume edit "$storage_pool" container/c1
    lxc storage volume show "$storage_pool" container/c1 | grep -xF 'description: bar'
    # edit the container snapshot volume name
    lxc storage volume show "$storage_pool" container/c1/s1 | \
        sed 's/^description:.*/description: baz/' | \
        lxc storage volume edit "$storage_pool" container/c1/s1
    lxc storage volume show "$storage_pool" container/c1/s1 | grep -xF 'description: baz'
    lxc delete c1
}

test_container_metadata() {
    ensure_import_testimage
    lxc init testimage c

    # metadata for the container are printed
    lxc config metadata show c | grep -wF BusyBox

    # metadata can be edited
    lxc config metadata show c | sed 's/BusyBox/BB/' | lxc config metadata edit c
    lxc config metadata show c | grep -wF BB

    # templates can be listed
    lxc config template list c | grep -F template.tpl

    # template content can be returned
    lxc config template show c template.tpl | grep -F "name:"

    # templates can be added
    lxc config template create c my.tpl
    lxc config template list c | grep -F my.tpl

    # templates cannot contain some illegal chars
    ! lxc config template create c foo/bar || false
    ! lxc config template create c foo..bar || false

    # template content can be updated
    echo "some content" | lxc config template edit c my.tpl
    lxc config template show c my.tpl | grep -F "some content"

    # templates can be removed
    lxc config template delete c my.tpl
    ! lxc config template list c | grep -F my.tpl || false

    lxc delete c
}

test_container_snapshot_config() {
    if ! tty -s; then
        echo "==> SKIP: test_container_snapshot_config requires a terminal"
        return
    fi

    ensure_import_testimage

    lxc init testimage foo -s "lxdtest-$(basename "${LXD_DIR}")"
    lxc snapshot foo
    lxc config show foo/snap0 | grep -F 'expires_at: 0001-01-01T00:00:00Z'

    echo 'expires_at: 2100-01-01T00:00:00Z' | lxc config edit foo/snap0
    lxc config show foo/snap0 | grep -F 'expires_at: 2100-01-01T00:00:00Z'

    # Remove expiry date using zero time
    echo 'expires_at: 0001-01-01T00:00:00Z' | lxc config edit foo/snap0
    lxc config show foo/snap0 | grep -F 'expires_at: 0001-01-01T00:00:00Z'

    echo 'expires_at: 2100-01-01T00:00:00Z' | lxc config edit foo/snap0
    lxc config show foo/snap0 | grep -F 'expires_at: 2100-01-01T00:00:00Z'

    # Remove expiry date using empty value
    echo 'expires_at:' | lxc config edit foo/snap0
    lxc config show foo/snap0 | grep -F 'expires_at: 0001-01-01T00:00:00Z'

    # Check instance name is included in edit screen.
    cmd=$(unset -f lxc; command -v lxc)
    output=$(EDITOR="cat" timeout --foreground 120 "${cmd}" config edit foo/snap0)
    echo "${output}" | grep "name: snap0"

    # Check expanded config isn't included in edit screen.
    ! echo "${output}"  | grep "expanded" || false

    lxc delete -f foo
}
