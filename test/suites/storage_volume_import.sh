test_storage_volume_import() {
  # Some storage types, such as lvm and ceph, support only 512-aligned image sizes
  # Test with weird sizes of images
  dd if=/dev/urandom of=foo.iso bs=359 count=1
  dd if=/dev/urandom of=foo.img bs=1M count=1
  echo -n "a" >> foo.img

  ensure_import_testimage

  # importing an ISO as storage volume requires a volume name
  ! lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.iso || false
  ! lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.img --type=iso || false

  # import ISO as storage volume
  lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.iso foo
  lxc storage volume import "lxdtest-$(basename "${LXD_DIR}")" ./foo.img --type=iso foobar
  lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")" foo | grep -xF 'content_type: iso'
  lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")" foobar | grep -xF 'content_type: iso'

  # check if storage volumes have an UUID.
  [ -n "$(lxc storage volume get "lxdtest-$(basename "${LXD_DIR}")" foo volatile.uuid)" ]
  [ -n "$(lxc storage volume get "lxdtest-$(basename "${LXD_DIR}")" foobar volatile.uuid)" ]

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

  # cannot change storage volume config
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

  rm -f foo.iso foo.img
}
