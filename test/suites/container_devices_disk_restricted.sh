test_container_devices_disk_restricted() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Create directory for use as basis for restricted disk source tests.
  testRoot="${TEST_DIR}/restricted"
  mkdir "${testRoot}"

  # Create directory for use as allowed disk source path prefix in project.
  mkdir "${testRoot}/allowed1"
  mkdir "${testRoot}/allowed2"
  touch "${testRoot}/allowed1/foo"
  mkdir "${testRoot}/not-allowed1"
  ln -s "${testRoot}/not-allowed1" "${testRoot}/allowed1/not-allowed1"
  ln -s "${testRoot}/allowed2" "${testRoot}/allowed1/not-allowed2"
  (cd "${testRoot}/allowed1" || false; ln -s foo foolink)


  # Create project with restricted disk source path.
  lxc project create restricted \
    -c features.images=false \
    -c restricted=true \
    -c restricted.devices.disk=allow \
    -c restricted.devices.disk.paths="${testRoot}/allowed1,${testRoot}/allowed2"
  lxc project switch restricted
  pool="lxdtest-$(basename "${LXD_DIR}")"
  lxc profile device add default root disk path="/" pool="${pool}"
  lxc profile show default

  # Create instance and add check relative source paths are not allowed.
  lxc init testimage c1
  ! lxc config device add c1 d1 disk source=foo path=/mnt || false

  # Check adding a disk with a source path above the restricted parent source path isn't allowed.
  ! lxc config device add c1 d1 disk source="${testRoot}/not-allowed1" path=/mnt || false

  # Check adding a disk with a source path that is a symlink above the restricted parent source path isn't allowed
  # at start time (check that openat2 restrictions are doing their job).
  lxc config device add c1 d1 disk source="${testRoot}/allowed1/not-allowed1" path=/mnt
  ! lxc start c1 || false

  # Check some rudimentary work arounds to allowed path checks don't work.
  ! lxc config device set c1 d1 source="${testRoot}/../not-allowed1" || false

  # Check adding a disk from a restricted source path cannot use shifting at start time. This is not safe as we
  # cannot prevent creation of files with setuid, which would allow a root executable to be created.
  lxc config device set c1 d1 source="${testRoot}/allowed1" shift=true
  ! lxc start c1 || false

  # Check adding a disk with a source path that is allowed is allowed.
  lxc config device set c1 d1 source="${testRoot}/allowed1" shift=false
  lxc start c1
  lxc exec c1 --project restricted -- ls /mnt/foo

  # Check adding a disk with a source path that is allowed that symlinks to another allowed source path isn't
  # allowed at start time.
  ! lxc config device set c1 d1 source="${testRoot}/allowed1/not-allowed2" || false

  # Check relative symlink inside allowed parent path is allowed.
  lxc stop -f c1
  lxc config device set c1 d1 source="${testRoot}/allowed1/foolink" path=/mnt/foolink
  lxc start c1
  lxc exec c1 --project restricted -- ls /mnt/foolink

  lxc delete -f c1
  lxc project switch default
  lxc project delete restricted
  rm "${testRoot}/allowed1/not-allowed1"
  rm "${testRoot}/allowed1/not-allowed2"
  rm "${testRoot}/allowed1/foo"
  rm "${testRoot}/allowed1/foolink"
  rmdir "${testRoot}/allowed1"
  rmdir "${testRoot}/allowed2"
  rmdir "${testRoot}/not-allowed1"
  rmdir "${testRoot}"
}
