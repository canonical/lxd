test_storage_profiles() {
  # shellcheck disable=2039

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
  spawn_lxd "${LXD_STORAGE_DIR}" false
  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"

    HAS_ZFS="dir"
    if which zfs >/dev/null 2>&1; then
      HAS_ZFS="zfs"
    fi

    HAS_BTRFS="dir"
    if which btrfs >/dev/null 2>&1; then
      HAS_BTRFS="btrfs"
    fi

    # shellcheck disable=SC1009
    # Create loop file zfs pool.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool1" "${HAS_ZFS}"

    # Create loop file btrfs pool.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool2" "${HAS_BTRFS}"

    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool4" dir

    # Set default storage pool for image import.
    lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Import image into default storage pool.
    ensure_import_testimage

    lxc profile create dummy

    # Create a new profile that provides a root device for some containers.
    lxc profile device add dummy rootfs disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Begin interesting test cases.

    for i in $(seq 1 3); do
      lxc launch testimage c"${i}" --profile dummy
    done

    # Check that we can't remove or change the root disk device since containers
    # are actually using it.
    ! lxc profile device remove dummy rootfs
    ! lxc profile device set dummy rootfs pool "lxdtest-$(basename "${LXD_DIR}")-pool2"

    # Give all the containers we started their own local root disk device.
    for i in $(seq 1 2); do
      ! lxc config device add c"${i}" root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"
      lxc config device add c"${i}" rootfs disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"
    done

    # Try to set new pool. This should fail since there is a single container
    # that has no local root disk device.
    ! lxc profile device set dummy rootfs pool "lxdtest-$(basename "${LXD_DIR}")-pool2"
    # This should work since it doesn't change the pool property.
    lxc profile device set dummy rootfs pool "lxdtest-$(basename "${LXD_DIR}")-pool1"
    # Check that we can not remove the root disk device since there is a single
    # container that is still using it.
    ! lxc profile device remove dummy rootfs

    # Give the last container a local root disk device.
    ! lxc config device add c3 root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"
    lxc config device add c3 rootfs disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Try to set new pool. This should work since the container has a local disk
    lxc profile device set dummy rootfs pool "lxdtest-$(basename "${LXD_DIR}")-pool2"
    lxc profile device set dummy rootfs pool "lxdtest-$(basename "${LXD_DIR}")-pool1"
    # Check that we can now remove the root disk device since no container is
    # actually using it.
    lxc profile device remove dummy rootfs

    # Add back a root device to the profile.
    ! lxc profile device add dummy rootfs1 disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Try to add another root device to the profile that tries to set a pool
    # property. This should fail. This is also a test for whether it is possible
    # to put multiple disk devices on the same path. This must fail!
    ! lxc profile device add dummy rootfs2 disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool2"

    # Add another root device to the profile that does not set a pool property.
    # This should not work since it would use the same path.
    ! lxc profile device add dummy rootfs3 disk path="/"

    # Create a second profile.
    lxc profile create dummyDup
    lxc profile device add dummyDup rootfs1 disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Create a third profile
    lxc profile create dummyNoDup
    lxc profile device add dummyNoDup rootfs2 disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool2"

    # Verify that we cannot create a container with profiles that have
    # contradicting root devices.
    ! lxc launch testimage cConflictingProfiles --p dummy -p dummyDup -p dummyNoDup

    # And that even with a local disk, a container can't have multiple root devices
    ! lxc launch testimage cConflictingProfiles -s "lxdtest-$(basename "${LXD_DIR}")-pool2" -p dummy -p dummyDup -p dummyNoDup

    # Check that we cannot assign conflicting profiles to a container that
    # relies on another profiles root disk device.
    lxc launch testimage cOnDefault
    ! lxc profile assign cOnDefault default,dummyDup,dummyNoDup

    # Verify that we can create a container with two profiles that speficy the
    # same root disk device.
    lxc launch testimage cNonConflictingProfiles -p dummy -p dummyDup

    # Try to remove the root disk device from one of the profiles.
    lxc profile device remove dummy rootfs1

    # Try to remove the root disk device from the second profile.
    ! lxc profile device remove dummyDup rootfs1

    # Test that we can't remove the root disk device from the containers config
    # when the profile it is attached to specifies no root device.
    for i in $(seq 1 3); do
      ! lxc config device remove c"${i}" root
      # Must fail.
      ! lxc profile assign c"${i}" dummyDup,dummyNoDup
    done

    lxc delete -f cNonConflictingProfiles
    lxc delete -f cOnDefault
    for i in $(seq 1 3); do
      lxc delete -f c"${i}"
    done

  )

  # shellcheck disable=SC2031
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}
