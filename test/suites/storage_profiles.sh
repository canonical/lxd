test_storage_profiles() {
  # shellcheck disable=2039

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  spawn_lxd "${LXD_STORAGE_DIR}" false
  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"
    lxd_backend="$(storage_backend "$LXD_DIR")"

    other_backend="btrfs"
    if [ "${lxd_backend}" = "${other_backend}" ]; then
      other_backend="dir"
    fi

    # Create a pool with the default backend.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool1" "${lxd_backend}"

    # Create another pool with a different backend.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool2" "${other_backend}"

    # Set default storage pool for image import.
    lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Import image into default storage pool.
    ensure_import_testimage

    lxc profile create test

    # Create a new profile that provides a root device for some containers.
    lxc profile device add test rootfs disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Begin interesting test cases.

    for i in $(seq 1 3); do
      lxc launch testimage c"${i}" --profile test
    done

    # Check that we can't remove or change the root disk device since containers
    # are actually using it.
    ! lxc profile device remove test rootfs || false
    ! lxc profile device set test rootfs pool "lxdtest-$(basename "${LXD_DIR}")-pool2" || false

    # Give all the containers we started their own local root disk device.
    for i in $(seq 1 2); do
      ! lxc config device add c"${i}" root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1" || false
      lxc config device add c"${i}" rootfs disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"
    done

    # Try to set new pool. This should fail since there is a single container
    # that has no local root disk device.
    ! lxc profile device set test rootfs pool "lxdtest-$(basename "${LXD_DIR}")-pool2" || false
    # This should work since it doesn't change the pool property.
    lxc profile device set test rootfs pool "lxdtest-$(basename "${LXD_DIR}")-pool1"
    # Check that we can not remove the root disk device since there is a single
    # container that is still using it.
    ! lxc profile device remove test rootfs || false

    # Give the last container a local root disk device.
    ! lxc config device add c3 root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1" || false
    lxc config device add c3 rootfs disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Try to set new pool. This should work since the container has a local disk
    lxc profile device set test rootfs pool "lxdtest-$(basename "${LXD_DIR}")-pool2"
    lxc profile device set test rootfs pool "lxdtest-$(basename "${LXD_DIR}")-pool1"
    # Check that we can now remove the root disk device since no container is
    # actually using it.
    lxc profile device remove test rootfs

    # Add back a root device to the profile.
    ! lxc profile device add test rootfs1 disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1" || false

    # Try to add another root device to the profile that tries to set a pool
    # property. This should fail. This is also a test for whether it is possible
    # to put multiple disk devices on the same path. This must fail!
    ! lxc profile device add test rootfs2 disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool2" || false

    # Add another root device to the profile that does not set a pool property.
    # This should not work since it would use the same path.
    ! lxc profile device add test rootfs3 disk path="/" || false

    # Create a second profile.
    lxc profile create testDup
    lxc profile device add testDup rootfs1 disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Create a third profile
    lxc profile create testNoDup
    lxc profile device add testNoDup rootfs2 disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool2"

    # Verify that we cannot create a container with profiles that have
    # contradicting root devices.
    ! lxc launch testimage cConflictingProfiles -p test -p testDup -p testNoDup || false

    # And that even with a local disk, a container can't have multiple root devices
    ! lxc launch testimage cConflictingProfiles -s "lxdtest-$(basename "${LXD_DIR}")-pool2" -p test -p testDup -p testNoDup || false

    # Check that we cannot assign conflicting profiles to a container that
    # relies on another profiles root disk device.
    lxc launch testimage cOnDefault
    ! lxc profile assign cOnDefault default,testDup,testNoDup || false

    # Verify that we can create a container with two profiles that specify the
    # same root disk device.
    lxc launch testimage cNonConflictingProfiles -p test -p testDup

    # Try to remove the root disk device from one of the profiles.
    lxc profile device remove test rootfs1

    # Try to remove the root disk device from the second profile.
    ! lxc profile device remove testDup rootfs1 || false

    # Test that we can't remove the root disk device from the containers config
    # when the profile it is attached to specifies no root device.
    for i in $(seq 1 3); do
      ! lxc config device remove c"${i}" root || false
      # Must fail.
      ! lxc profile assign c"${i}" testDup,testNoDup || false
    done

    # Create a new profile and volume for testing custom block volume sharing.
    lxc profile create volumeSharingTest
    lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool1" block-vol --type=block

    # Test adding a non-shared block volume device to a profile. That operation must fail.
    ! lxc profile device add volumeSharingTest test-disk disk pool="lxdtest-$(basename "${LXD_DIR}")-pool1" source=block-vol || false

    # Then enabling sharing the block volume and trying again, must succeed this time.
    lxc storage volume set "lxdtest-$(basename "${LXD_DIR}")-pool1" block-vol security.shared true
    lxc profile device add volumeSharingTest test-disk disk pool="lxdtest-$(basename "${LXD_DIR}")-pool1" source=block-vol

    # Try to disable security.shared for a volume already added to a profile. That operation must fail.
    ! lxc storage volume set "lxdtest-$(basename "${LXD_DIR}")-pool1" block-vol security.shared false || false

    # Cleaning everything added during the last tests
    lxc profile device remove volumeSharingTest test-disk
    lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool1" block-vol
    lxc profile delete volumeSharingTest

    lxc delete -f cNonConflictingProfiles
    lxc delete -f cOnDefault
    for i in $(seq 1 3); do
      lxc delete -f c"${i}"
    done

  )

  # shellcheck disable=SC2031,2269
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}
