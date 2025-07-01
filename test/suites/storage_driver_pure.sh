test_storage_driver_pure() {
  local lxd_backend

  lxd_backend=$(storage_backend "${LXD_DIR}")
  if [ "${lxd_backend}" != "pure" ]; then
    echo "==> SKIP: test_storage_driver_pure only supports 'pure', not ${lxd_backend}"
    return
  fi

  local LXD_STORAGE_DIR

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  spawn_lxd "${LXD_STORAGE_DIR}" false

  (
    set -eux
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"

    # Create 2 storage pools.
    poolName1="lxdtest-$(basename "${LXD_DIR}")-pool1"
    poolName2="lxdtest-$(basename "${LXD_DIR}")-pool2"
    configure_pure_pool "${poolName1}"
    configure_pure_pool "${poolName2}"

    # Configure default volume size for pools.
    lxc storage set "${poolName1}" volume.size=25MiB
    lxc storage set "${poolName2}" volume.size=25MiB

    # Set default storage pool for image import.
    lxc profile device add default root disk path="/" pool="${poolName1}"

    # Import image into default storage pool.
    ensure_import_testimage

    # Muck around with some containers on various pools.
    lxc init testimage c1pool1 -s "${poolName1}"
    lxc list -c b c1pool1 | grep "${poolName1}"

    lxc init testimage c2pool2 -s "${poolName2}"
    lxc list -c b c2pool2 | grep "${poolName2}"

    lxc launch images:alpine/edge c3pool1 -s "${poolName1}"
    lxc list -c b c3pool1 | grep "${poolName1}"

    lxc launch images:alpine/edge c4pool2 -s "${poolName2}"
    lxc list -c b c4pool2 | grep "${poolName2}"

    lxc storage set "${poolName1}" volume.block.filesystem xfs
    lxc storage set "${poolName1}" volume.size 300MiB # modern xfs requires 300MiB or more
    lxc init testimage c5pool1 -s "${poolName1}"

    # Test whether dependency tracking is working correctly. We should be able
    # to create a container, copy it, which leads to a dependency relation
    # between the source container's storage volume and the copied container's
    # storage volume. Now, we delete the source container which will trigger a
    # rename operation and not an actual delete operation. Now we create a
    # container of the same name as the source container again, create a copy of
    # it to introduce another dependency relation. Now we delete the source
    # container again. This should work. If it doesn't it means the rename
    # operation tries to map the two source to the same name.
    lxc init testimage a -s "${poolName1}"
    lxc copy a b
    lxc delete a
    lxc init testimage a -s "${poolName1}"
    lxc copy a c
    lxc delete a
    lxc delete b
    lxc delete c

    lxc storage volume create "${poolName1}" c1pool1
    lxc storage volume attach "${poolName1}" c1pool1 c1pool1 testDevice /opt
    ! lxc storage volume attach "${poolName1}" c1pool1 c1pool1 testDevice2 /opt || false
    lxc storage volume detach "${poolName1}" c1pool1 c1pool1
    lxc storage volume attach "${poolName1}" custom/c1pool1 c1pool1 testDevice /opt
    ! lxc storage volume attach "${poolName1}" custom/c1pool1 c1pool1 testDevice2 /opt || false
    lxc storage volume detach "${poolName1}" c1pool1 c1pool1

    lxc storage volume create "${poolName1}" c2pool2
    lxc storage volume attach "${poolName1}" c2pool2 c2pool2 testDevice /opt
    ! lxc storage volume attach "${poolName1}" c2pool2 c2pool2 testDevice2 /opt || false
    lxc storage volume detach "${poolName1}" c2pool2 c2pool2
    lxc storage volume attach "${poolName1}" custom/c2pool2 c2pool2 testDevice /opt
    ! lxc storage volume attach "${poolName1}" custom/c2pool2 c2pool2 testDevice2 /opt || false
    lxc storage volume detach "${poolName1}" c2pool2 c2pool2

    lxc storage volume create "${poolName2}" c3pool1
    lxc storage volume attach "${poolName2}" c3pool1 c3pool1 testDevice /opt
    ! lxc storage volume attach "${poolName2}" c3pool1 c3pool1 testDevice2 /opt || false
    lxc storage volume detach "${poolName2}" c3pool1 c3pool1
    lxc storage volume attach "${poolName2}" c3pool1 c3pool1 testDevice /opt
    ! lxc storage volume attach "${poolName2}" c3pool1 c3pool1 testDevice2 /opt || false
    lxc storage volume detach "${poolName2}" c3pool1 c3pool1

    lxc storage volume create "${poolName2}" c4pool2
    lxc storage volume attach "${poolName2}" c4pool2 c4pool2 testDevice /opt
    ! lxc storage volume attach "${poolName2}" c4pool2 c4pool2 testDevice2 /opt || false
    lxc storage volume detach "${poolName2}" c4pool2 c4pool2
    lxc storage volume attach "${poolName2}" custom/c4pool2 c4pool2 testDevice /opt
    ! lxc storage volume attach "${poolName2}" custom/c4pool2 c4pool2 testDevice2 /opt || false
    lxc storage volume detach "${poolName2}" c4pool2 c4pool2
    lxc storage volume rename "${poolName2}" c4pool2 c4pool2-renamed
    lxc storage volume rename "${poolName2}" c4pool2-renamed c4pool2

    lxc delete -f c1pool1
    lxc delete -f c3pool1
    lxc delete -f c5pool1

    lxc delete -f c4pool2
    lxc delete -f c2pool2

    lxc storage volume set "${poolName1}" c1pool1 size 500MiB
    lxc storage volume unset "${poolName1}" c1pool1 size

    lxc storage volume delete "${poolName1}" c1pool1
    lxc storage volume delete "${poolName1}" c2pool2
    lxc storage volume delete "${poolName2}" c3pool1
    lxc storage volume delete "${poolName2}" c4pool2

    lxc image delete testimage
    lxc profile device remove default root
    lxc storage delete "${poolName1}"
    lxc storage delete "${poolName2}"
  )

  # shellcheck disable=SC2031
  kill_lxd "${LXD_STORAGE_DIR}"
}
