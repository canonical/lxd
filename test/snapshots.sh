test_snapshots() {
  lxc init testimage foo

  lxc snapshot foo
  [ -d "$LXD_DIR/lxc/foo/snapshots/snap0" ]

  lxc snapshot foo
  [ -d "$LXD_DIR/lxc/foo/snapshots/snap1" ]

  lxc snapshot foo tester
  [ -d "$LXD_DIR/lxc/foo/snapshots/tester" ]

  lxc delete foo/snap0
  [ ! -d "$LXD_DIR/lxc/foo/snapshots/snap0" ]

  # no CLI for this, so we use the API directly
  wait_for my_curl -X POST $BASEURL/1.0/containers/foo/snapshots/tester -d "{\"name\":\"tester2\"}"
  [ ! -d "$LXD_DIR/lxc/foo/snapshots/tester" ]

  # no CLI for this, so we use the API directly
  wait_for my_curl -X DELETE $BASEURL/1.0/containers/foo/snapshots/tester2
  [ ! -d "$LXD_DIR/lxc/foo/snapshots/tester2" ]

  lxc delete foo
  [ ! -d "$LXD_DIR/lxc/foo" ]
}
