test_storage_volume_import() {
  # Some storage types, such as lvm and ceph, support only 512-aligned image sizes
  # Test with weird sizes of images
  dd if=/dev/urandom of=foo.iso bs=359 count=1
  dd if=/dev/urandom of=foo.img bs=1M count=1
  echo -n "a" >> foo.img
  tar -czf foo.tar.gz foo.iso

  ensure_import_testimage

  # importing an ISO or tarball as storage volume requires a volume name
  ! lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.iso || false
  ! lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.img --type=iso || false
  ! lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.tar.gz --type=tar || false

  # import ISO as storage volume
  lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.iso foo
  lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.img --type=iso foobar
  lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")" foo | grep -xF 'content_type: iso'
  lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")" foobar | grep -xF 'content_type: iso'

  # importing ISO again under the same name should fail as the target volume already exists
  ! lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.iso foo || false

  # import tarball as storage volume
  lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.tar.gz tar --type=tar
  lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")" tar | grep -xF 'content_type: filesystem'

  # importing tarball again under the same name should fail as the target volume already exists
  ! lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.tar.gz tar --type=tar || false

  # check if storage volumes have an UUID.
  [ -n "$(lxc storage volume get "lxdtest-$(basename "${LXD_DIR}")" foo volatile.uuid)" ]
  [ -n "$(lxc storage volume get "lxdtest-$(basename "${LXD_DIR}")" foobar volatile.uuid)" ]
  [ -n "$(lxc storage volume get "lxdtest-$(basename "${LXD_DIR}")" tar volatile.uuid)" ]

  # delete an ISO storage volume and re-import it
  lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")" foo
  lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")" foobar

  lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.iso foo
  lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.img --type=iso foobar
  lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")" foo | grep -xF 'content_type: iso'
  lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")" foobar | grep -xF 'content_type: iso'

  # snapshots are disabled for ISO storage volumes
  ! lxc storage volume snapshot "lxdtest-$(basename "${LXD_DIR}")" foo || false

  # backups are disabled for ISO storage volumes
  ! lxc storage volume export "lxdtest-$(basename "${LXD_DIR}")" foo || false

  # cannot attach ISO storage volumes to containers
  lxc init testimage c1
  ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")" foo c1 || false

  # Attach storage volume created from tarball
  lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")" tar c1 /tar

  # Start the container and check if the tarball content is available
  lxc start c1
  lxc exec c1 -- ls /tar | grep -xF 'foo.iso'
  lxc stop -f c1
  lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")" tar c1

  # cannot change ISO storage volume config
  ! lxc storage volume set "lxdtest-$(basename "${LXD_DIR}")" foo size=1GiB || false

  # copy volume
  lxc storage volume copy "lxdtest-$(basename "${LXD_DIR}")"/foo "lxdtest-$(basename "${LXD_DIR}")"/bar
  lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")" bar | grep -xF 'content_type: iso'

  # cannot refresh copy
  ! lxc storage volume copy "lxdtest-$(basename "${LXD_DIR}")"/foo "lxdtest-$(basename "${LXD_DIR}")"/bar --refresh || false

  # can change description
  lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")" foo | sed 's/^description:.*/description: foo/' | lxc storage volume edit "lxdtest-$(basename "${LXD_DIR}")" foo
  lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")" foo | grep -xF 'description: foo'

  # cleanup
  lxc delete -f c1
  lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")" foo
  lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")" bar
  lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")" foobar
  lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")" tar

  rm -f foo.iso foo.img foo.tar.gz
}
