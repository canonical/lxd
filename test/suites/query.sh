test_query() {
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc init --empty querytest
  lxc query --wait -X POST -d '{"name": "snap-test"}' /1.0/containers/querytest/snapshots
  lxc info querytest | grep snap-test
  lxc delete querytest
}
