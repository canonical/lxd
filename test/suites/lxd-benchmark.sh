_lxd_benchmark(){
  local c="${1}"
  local p="${2}"
  ensure_import_testimage
  lxd-benchmark launch --count "${c}" --parallel "${p}" testimage
  lxd-benchmark delete --parallel "${p}"
}

test_lxd_benchmark_basic(){
  ensure_import_testimage
  lxd-benchmark init --count 5 testimage
  lxd-benchmark start
  lxd-benchmark stop
  lxd-benchmark delete
}

test_lxd_benchmark_serial() {
  _lxd_benchmark 50 1
}

test_lxd_benchmark_parallel() {
  _lxd_benchmark 50 10
}
