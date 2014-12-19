test_snapshots() {
  lxc create images:ubuntu foo

  lxc snapshot foo tester
  [ -d "$LXD_DIR/lxc/foo/snapshots/tester" ]

  # no CLI for this, so we use the API directly
  wait_for my_curl -X POST $BASEURL/1.0/containers/foo/snapshots/tester -d "{\"name\":\"tester2\"}"
  [ ! -d "$LXD_DIR/lxc/foo/snapshots/tester" ]

  # no CLI for this, so we use the API directly
  wait_for my_curl -X DELETE $BASEURL/1.0/containers/foo/snapshots/tester2
  [ ! -d "$LXD_DIR/lxc/foo/snapshots/tester2" ]
}
