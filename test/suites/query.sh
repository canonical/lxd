test_query() {
  lxc init --empty querytest
  lxc query --wait -X POST -d '{"name": "snap-test"}' /1.0/containers/querytest/snapshots
  lxc info querytest | grep -wF snap-test
  lxc delete querytest
}
